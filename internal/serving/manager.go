// Package serving manages real model-serving containers on the local Docker
// engine: the Compose-native fulfilment of the PRD inference engine (KServe
// remains the Kubernetes path). Each deployed model version runs
// `mlflow models serve` in its own container on the platform network.
package serving

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Manager struct {
	// APIVersion pins the Docker Engine API version (e.g. "v1.44"). Empty
	// uses unversioned paths, which the daemon serves at its newest version.
	APIVersion string

	// BaseURL of the Docker Engine API. Empty means the default unix socket.
	BaseURL string
	// SocketPath for unix transport when BaseURL is empty.
	SocketPath string
	// Image run for every deployment (must contain mlflow + S3 deps).
	Image string
	// Network the container joins so the gateway can reach it by name.
	Network string
	// Env passed to every serving container (MLflow tracking + S3 credentials).
	Env []string
	// Port the model server listens on inside the container.
	Port int

	client *http.Client
}

type Deployment struct {
	Name        string `json:"name"`
	ArtifactURI string `json:"artifact_uri"`
	Endpoint    string `json:"endpoint"`
	State       string `json:"state"`
}

func NewManager(image, network string, env []string) *Manager {
	return &Manager{SocketPath: "/var/run/docker.sock", Image: image, Network: network, Env: env, Port: 5001}
}

func (m *Manager) httpClient() *http.Client {
	if m.client != nil {
		return m.client
	}
	if m.BaseURL != "" {
		m.client = &http.Client{Timeout: 60 * time.Second}
	} else {
		m.client = &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", m.SocketPath)
			}},
		}
	}
	return m.client
}

func (m *Manager) do(ctx context.Context, method, path string, input any, output any) (int, error) {
	base := m.BaseURL
	if base == "" {
		base = "http://docker"
	}
	if m.APIVersion != "" {
		base += "/" + m.APIVersion
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return 0, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := m.httpClient().Do(request)
	if err != nil {
		return 0, fmt.Errorf("docker engine unreachable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 400 && response.StatusCode != http.StatusNotFound {
		return response.StatusCode, fmt.Errorf("docker engine returned %s: %s", response.Status, strings.TrimSpace(string(raw)))
	}
	if output != nil && len(raw) > 0 && response.StatusCode < 300 {
		if err := json.Unmarshal(raw, output); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func containerName(deployment string) string { return "mlaiops-serve-" + deployment }

// Deploy runs a serving container for the artifact and returns its in-network
// endpoint. An existing deployment with the same name is replaced, which is
// what makes rollback and re-deploy idempotent.
func (m *Manager) Deploy(ctx context.Context, name, artifactURI string) (string, error) {
	if name == "" || artifactURI == "" {
		return "", errors.New("name and artifact_uri are required")
	}
	if m.Image == "" {
		return "", errors.New("serving image is not configured")
	}
	_ = m.Undeploy(ctx, name)
	create := map[string]any{
		"Image": m.Image,
		"Cmd": []string{
			"mlflow", "models", "serve",
			"-m", artifactURI,
			"-h", "0.0.0.0",
			"-p", fmt.Sprintf("%d", m.Port),
			"--env-manager", "local",
		},
		"Env": m.Env,
		"Labels": map[string]string{
			"mlaiops.serving":  "true",
			"mlaiops.model":    name,
			"mlaiops.artifact": artifactURI,
		},
		"HostConfig": map[string]any{"NetworkMode": m.Network, "RestartPolicy": map[string]any{"Name": "unless-stopped"}},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if _, err := m.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(containerName(name)), create, &created); err != nil {
		return "", err
	}
	if _, err := m.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%d", containerName(name), m.Port), nil
}

// Undeploy stops and removes the deployment container. Missing containers are
// not an error.
func (m *Manager) Undeploy(ctx context.Context, name string) error {
	status, err := m.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(containerName(name))+"?force=true", nil, nil)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

// List returns the platform's serving deployments from container labels.
func (m *Manager) List(ctx context.Context) ([]Deployment, error) {
	filters := url.QueryEscape(`{"label":["mlaiops.serving=true"]}`)
	var containers []struct {
		Labels map[string]string `json:"Labels"`
		State  string            `json:"State"`
	}
	if _, err := m.do(ctx, http.MethodGet, "/containers/json?all=true&filters="+filters, nil, &containers); err != nil {
		return nil, err
	}
	deployments := make([]Deployment, 0, len(containers))
	for _, container := range containers {
		name := container.Labels["mlaiops.model"]
		deployments = append(deployments, Deployment{
			Name:        name,
			ArtifactURI: container.Labels["mlaiops.artifact"],
			Endpoint:    fmt.Sprintf("http://%s:%d", containerName(name), m.Port),
			State:       container.State,
		})
	}
	return deployments, nil
}

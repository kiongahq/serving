// Package serving manages real model-serving containers on the local Docker
// engine: the Compose-native fulfilment of the inference engine (KServe
// remains the Kubernetes path). Each deployed model version runs
// `mlflow models serve` in its own container on the platform network.
package serving

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	defaultPidsLimit int64 = 256
	maximumPidsLimit int64 = 4096
)

type Manager struct {
	// APIVersion pins the Docker Engine API version (e.g. "v1.44"). Empty
	// uses unversioned paths, which the daemon serves at its newest version.
	APIVersion string

	// BaseURL of the Docker Engine API. Empty means the default unix socket.
	BaseURL string
	// SocketPath for unix transport when BaseURL is empty.
	SocketPath string
	// Image is the fallback for deployments that do not register a model-specific
	// serving image. It must contain MLflow, artifact-store support, and the model's
	// framework dependencies.
	Image string
	// Network the container joins so the gateway can reach it by name.
	Network string
	// Env passed to every serving container (MLflow tracking + S3 credentials).
	Env []string
	// Port the model server listens on inside the container.
	Port int
	// PidsLimit bounds processes and threads in each untrusted serving container.
	PidsLimit int64

	client *http.Client
}

type Deployment struct {
	Name         string `json:"name"`
	ArtifactURI  string `json:"artifact_uri"`
	ServingImage string `json:"serving_image,omitempty"`
	Endpoint     string `json:"endpoint"`
	State        string `json:"state"`
}

func NewManager(image, network string, env []string) *Manager {
	return &Manager{SocketPath: "/var/run/docker.sock", Image: image, Network: network, Env: env, Port: 5001, PidsLimit: defaultPidsLimit}
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

const containerPrefix = "mlaiops-serve-"

// containerName preserves existing DNS-safe model names. Unsafe or overlong names
// receive a readable slug plus a stable hash so Docker and platform DNS always see
// a valid, collision-resistant label without changing the model's API name.
func containerName(deployment string) string {
	original := strings.TrimSpace(deployment)
	lower := strings.ToLower(original)
	changed := lower != original
	var slug strings.Builder
	lastHyphen := false
	for _, char := range lower {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			slug.WriteRune(char)
			lastHyphen = false
			continue
		}
		changed = changed || char != '-'
		if slug.Len() > 0 && !lastHyphen {
			slug.WriteByte('-')
			lastHyphen = true
		}
	}
	value := strings.Trim(slug.String(), "-")
	changed = changed || value != lower
	maxSegment := 63 - len(containerPrefix)
	if !changed && len(value) <= maxSegment {
		return containerPrefix + value
	}
	if value == "" {
		value = "model"
	}
	hash := sha256.Sum256([]byte(original))
	suffix := fmt.Sprintf("-%x", hash[:6])
	if len(value) > maxSegment-len(suffix) {
		value = strings.TrimRight(value[:maxSegment-len(suffix)], "-")
	}
	return containerPrefix + value + suffix
}

func normalizeImage(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("serving_image must not contain control characters")
	}
	image := strings.TrimSpace(value)
	if image == "" {
		return "", errors.New("serving_image must not be whitespace-only")
	}
	if strings.IndexFunc(image, unicode.IsSpace) >= 0 {
		return "", errors.New("serving_image must not contain whitespace")
	}
	return image, nil
}

// Deploy runs a serving container for the artifact and returns its in-network
// endpoint. servingImage selects a framework-compatible runtime for this model;
// the manager's configured Image remains the fallback. An existing deployment
// with the same name is replaced, which makes rollback and re-deploy idempotent.
func (m *Manager) Deploy(ctx context.Context, name, artifactURI, servingImage string) (string, error) {
	name = strings.TrimSpace(name)
	artifactURI = strings.TrimSpace(artifactURI)
	if name == "" || artifactURI == "" {
		return "", errors.New("name and artifact_uri are required")
	}
	image, err := normalizeImage(servingImage)
	if err != nil {
		return "", err
	}
	if image == "" {
		image, err = normalizeImage(m.Image)
		if err != nil {
			return "", err
		}
	}
	if image == "" {
		return "", errors.New("serving image is not configured")
	}
	pidsLimit := m.PidsLimit
	if pidsLimit <= 0 {
		pidsLimit = defaultPidsLimit
	}
	if pidsLimit > maximumPidsLimit {
		pidsLimit = maximumPidsLimit
	}
	_ = m.Undeploy(ctx, name)
	create := map[string]any{
		"Image": image,
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
			"mlaiops.image":    image,
		},
		"HostConfig": map[string]any{
			"NetworkMode":   m.Network,
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
			"CapDrop":       []string{"ALL"},
			"SecurityOpt":   []string{"no-new-privileges"},
			"PidsLimit":     pidsLimit,
		},
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
			Name:         name,
			ArtifactURI:  container.Labels["mlaiops.artifact"],
			ServingImage: container.Labels["mlaiops.image"],
			Endpoint:     fmt.Sprintf("http://%s:%d", containerName(name), m.Port),
			State:        container.State,
		})
	}
	return deployments, nil
}

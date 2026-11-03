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
	// DefaultReadyTimeout keeps a deploy inside the platform gateway's 90-second
	// call to the manager.
	DefaultReadyTimeout = 80 * time.Second
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
	// serving image. It must contain MLflow and the model's framework
	// dependencies.
	Image string
	// FetchImage runs the trusted artifact download step. It must contain MLflow
	// with artifact-store support. Empty uses Image. A request's serving_image is
	// never used here, because this container holds the store credentials.
	FetchImage string
	// Network the container joins so the gateway can reach it by name.
	Network string
	// FetchEnv holds the MLflow tracking and object-store credentials. It is
	// passed only to the short-lived fetch container that runs FetchImage with a
	// fixed command; model-serving containers never receive it.
	FetchEnv []string
	// Port the model server listens on inside the container.
	Port int
	// PidsLimit bounds processes and threads in each untrusted serving container.
	PidsLimit int64

	// ReadyTimeout bounds a whole deploy, from the artifact download until the
	// model is ready to serve. Zero uses DefaultReadyTimeout.
	ReadyTimeout time.Duration

	client *http.Client
	// now and sleep are replaced by tests to drive polling deterministically.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

type Deployment struct {
	Name         string `json:"name"`
	ArtifactURI  string `json:"artifact_uri"`
	ServingImage string `json:"serving_image,omitempty"`
	Endpoint     string `json:"endpoint"`
	State        string `json:"state"`
}

// NewManager returns a manager that serves with image on network. fetchEnv is
// the credential environment for the trusted artifact fetch step only.
func NewManager(image, network string, fetchEnv []string) *Manager {
	return &Manager{SocketPath: "/var/run/docker.sock", Image: image, Network: network, FetchEnv: fetchEnv, Port: 5001, PidsLimit: defaultPidsLimit}
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

// ErrNotFound reports a Docker Engine 404: a missing image, container or
// volume. Only callers for which absence is success (Undeploy) may ignore it.
var ErrNotFound = errors.New("not found on the docker engine")

// EngineError is a Docker Engine response with an error status.
type EngineError struct {
	Status int
	Body   string
}

func (e *EngineError) Error() string {
	return fmt.Sprintf("docker engine returned %d %s: %s", e.Status, http.StatusText(e.Status), e.Body)
}

// Is makes errors.Is(err, ErrNotFound) true for a 404.
func (e *EngineError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

func (m *Manager) do(ctx context.Context, method, path string, input any, output any) (int, error) {
	status, raw, err := m.doRaw(ctx, method, path, input)
	if err != nil {
		return status, err
	}
	if output != nil && len(raw) > 0 && status < 300 {
		if err := json.Unmarshal(raw, output); err != nil {
			return status, err
		}
	}
	return status, nil
}

// doRaw sends one Engine API call and returns the status and up to 1 MiB of the
// response body. Any status of 400 or above is an *EngineError.
func (m *Manager) doRaw(ctx context.Context, method, path string, input any) (int, []byte, error) {
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
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := m.httpClient().Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("docker engine unreachable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 400 {
		return response.StatusCode, raw, &EngineError{Status: response.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	return response.StatusCode, raw, nil
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
	// A deploy that does not reach a live endpoint leaves nothing behind.
	live := false
	defer func() {
		if !live {
			_ = m.Undeploy(context.WithoutCancel(ctx), name)
		}
	}()
	timeout := m.ReadyTimeout
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	deadline := m.clock().Add(timeout)
	if err := m.fetchArtifact(ctx, name, artifactURI, pidsLimit, deadline); err != nil {
		return "", err
	}
	// The serving container runs untrusted model code: it gets the artifact as a
	// read-only volume and no environment, so no store or tracking credentials.
	create := map[string]any{
		"Image": image,
		"Cmd": []string{
			"mlflow", "models", "serve",
			"-m", modelPath,
			"-h", "0.0.0.0",
			"-p", fmt.Sprintf("%d", m.Port),
			"--env-manager", "local",
		},
		"Env": []string{},
		"Labels": map[string]string{
			"mlaiops.serving":  "true",
			"mlaiops.model":    name,
			"mlaiops.artifact": artifactURI,
			"mlaiops.image":    image,
		},
		"HostConfig": hostConfig(m.Network, pidsLimit, []map[string]any{
			{"Type": "volume", "Source": volumeName(name), "Target": artifactMount, "ReadOnly": true},
		}, "unless-stopped"),
	}
	var created struct {
		ID string `json:"Id"`
	}
	if _, err := m.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(containerName(name)), create, &created); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("serving image %q is not present on the docker engine: %w", image, err)
		}
		return "", fmt.Errorf("create serving container: %w", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", errors.New("docker engine created a container without an ID")
	}
	if _, err := m.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(created.ID)+"/start", nil, nil); err != nil {
		return "", fmt.Errorf("start serving container: %w", err)
	}
	live = true
	return fmt.Sprintf("http://%s:%d", containerName(name), m.Port), nil
}

// Undeploy stops and removes the deployment container, any leftover artifact
// fetch container and the model artifact volume. Missing resources are not an
// error.
func (m *Manager) Undeploy(ctx context.Context, name string) error {
	if err := m.removeContainer(ctx, containerName(name)); err != nil {
		return err
	}
	if err := m.removeContainer(ctx, fetchContainerName(name)); err != nil {
		return err
	}
	_, err := m.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(volumeName(name))+"?force=true", nil, nil)
	if errors.Is(err, ErrNotFound) {
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

package serving

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// artifactMount is where the model artifact volume is mounted in the fetch
	// container (read-write) and in the serving container (read-only).
	artifactMount = "/opt/kionga/artifact"
	// modelPath is the MLflow model directory inside artifactMount.
	modelPath = artifactMount + "/model"
	// logTailLines is how much container output a failed deploy reports.
	logTailLines = 50
	pollInterval = time.Second
)

// fetchScript downloads the artifact named by argv[1] with MLflow's own
// resolver (models:/, runs:/, s3:// and the other schemes MLflow supports) and
// leaves the model directory at modelPath. The URI is an argument, never
// interpolated into code. It downloads files only: it never imports or
// unpickles the model, so untrusted model code does not run with credentials.
const fetchScript = `import os, sys
from mlflow.artifacts import download_artifacts
root = "` + artifactMount + `"
path = download_artifacts(artifact_uri=sys.argv[1], dst_path=os.path.join(root, "download"))
if not os.path.isfile(os.path.join(path, "MLmodel")):
    sys.exit("artifact has no MLmodel file: " + sys.argv[1])
os.replace(path, "` + modelPath + `")
`

func volumeName(deployment string) string { return containerName(deployment) + "-model" }

func fetchContainerName(deployment string) string { return containerName(deployment) + "-fetch" }

// containerState is the part of GET /containers/{id}/json the manager reads.
type containerState struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status   string `json:"Status"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Manager) pause(ctx context.Context, d time.Duration) error {
	if m.sleep != nil {
		return m.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Manager) inspect(ctx context.Context, id string) (containerState, error) {
	var state containerState
	_, err := m.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &state)
	return state, err
}

// waitFor polls the container until check reports done or an error, or until
// deadline passes on the manager's clock.
func (m *Manager) waitFor(ctx context.Context, id string, deadline time.Time, check func(containerState) (bool, error)) error {
	for {
		state, err := m.inspect(ctx, id)
		if err != nil {
			return err
		}
		done, err := check(state)
		if err != nil || done {
			return err
		}
		if !m.clock().Before(deadline) {
			return errTimeout
		}
		if err := m.pause(ctx, pollInterval); err != nil {
			return err
		}
	}
}

var errTimeout = errors.New("timed out")

// logTail returns the last lines of a container's stdout and stderr, decoded
// from the Engine's multiplexed stream. It returns "" when logs are unavailable.
func (m *Manager) logTail(ctx context.Context, id string) string {
	_, raw, err := m.doRaw(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&tail=%d", url.PathEscape(id), logTailLines), nil)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(demuxLogs(raw))
}

// demuxLogs strips the 8-byte frame headers Docker adds to non-TTY log
// streams. Input that is not framed is returned unchanged.
func demuxLogs(raw []byte) string {
	var out strings.Builder
	rest := raw
	for len(rest) >= 8 {
		stream := rest[0]
		if stream > 2 || rest[1] != 0 || rest[2] != 0 || rest[3] != 0 {
			return string(raw)
		}
		size := int(binary.BigEndian.Uint32(rest[4:8]))
		if size > len(rest)-8 {
			return string(raw)
		}
		out.Write(rest[8 : 8+size])
		rest = rest[8+size:]
	}
	if len(rest) > 0 {
		return string(raw)
	}
	return out.String()
}

func withLogs(err error, logs string) error {
	if logs == "" {
		return err
	}
	return fmt.Errorf("%w; last container output:\n%s", err, logs)
}

func hostConfig(network string, pidsLimit int64, mounts []map[string]any, restart string) map[string]any {
	return map[string]any{
		"NetworkMode":   network,
		"RestartPolicy": map[string]any{"Name": restart},
		"CapDrop":       []string{"ALL"},
		"SecurityOpt":   []string{"no-new-privileges"},
		"PidsLimit":     pidsLimit,
		"Mounts":        mounts,
	}
}

func (m *Manager) removeContainer(ctx context.Context, name string) error {
	_, err := m.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// fetchArtifact downloads the artifact into a fresh volume with the trusted
// fetch image and the store credentials, then removes the fetch container. The
// serving container later mounts the volume read-only and gets no credentials.
func (m *Manager) fetchArtifact(ctx context.Context, name, artifactURI string, pidsLimit int64, deadline time.Time) error {
	fetchImage, err := normalizeImage(m.FetchImage)
	if err != nil {
		return err
	}
	if fetchImage == "" {
		if fetchImage, err = normalizeImage(m.Image); err != nil {
			return err
		}
	}
	if fetchImage == "" {
		return errors.New("artifact fetch image is not configured")
	}
	labels := map[string]string{"mlaiops.serving.artifact": "true", "mlaiops.model": name}
	if _, err := m.do(ctx, http.MethodPost, "/volumes/create", map[string]any{"Name": volumeName(name), "Labels": labels}, nil); err != nil {
		return fmt.Errorf("create artifact volume: %w", err)
	}
	fetchName := fetchContainerName(name)
	_ = m.removeContainer(ctx, fetchName)
	create := map[string]any{
		"Image":  fetchImage,
		"Cmd":    []string{"python", "-c", fetchScript, artifactURI},
		"Env":    m.FetchEnv,
		"Labels": labels,
		"HostConfig": hostConfig(m.Network, pidsLimit, []map[string]any{
			{"Type": "volume", "Source": volumeName(name), "Target": artifactMount},
		}, "no"),
	}
	var created struct {
		ID string `json:"Id"`
	}
	if _, err := m.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(fetchName), create, &created); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("artifact fetch image %q is not present on the docker engine: %w", fetchImage, err)
		}
		return fmt.Errorf("create artifact fetch container: %w", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return errors.New("docker engine created a fetch container without an ID")
	}
	// The fetch container is removed whatever happens; it holds credentials.
	defer func() { _ = m.removeContainer(context.WithoutCancel(ctx), created.ID) }()
	if _, err := m.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(created.ID)+"/start", nil, nil); err != nil {
		return fmt.Errorf("start artifact fetch container: %w", err)
	}
	err = m.waitFor(ctx, created.ID, deadline, func(state containerState) (bool, error) {
		switch state.State.Status {
		case "exited", "dead":
			if state.State.ExitCode != 0 {
				return true, fmt.Errorf("artifact fetch for %q exited with code %d", artifactURI, state.State.ExitCode)
			}
			return true, nil
		}
		return false, nil
	})
	if errors.Is(err, errTimeout) {
		err = fmt.Errorf("artifact fetch for %q timed out", artifactURI)
	}
	if err != nil {
		return withLogs(err, m.logTail(context.WithoutCancel(ctx), created.ID))
	}
	return nil
}

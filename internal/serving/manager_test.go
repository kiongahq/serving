package serving

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var fetchEnv = []string{
	"MLFLOW_TRACKING_URI=http://mlflow:5000",
	"MLFLOW_S3_ENDPOINT_URL=http://objectstore:9000",
	"AWS_ACCESS_KEY_ID=platform-key",
	"AWS_SECRET_ACCESS_KEY=platform-secret",
}

func testManager(baseURL string) *Manager {
	manager := NewManager("mlaiops-mlflow", "mlaiops_default", fetchEnv)
	manager.BaseURL = baseURL
	return manager
}

const servingName = "mlaiops-serve-churn"

func TestDeployCreatesAndStartsContainer(t *testing.T) {
	e, server := newFakeEngine(t)
	endpoint, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn-classifier/3", "")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://mlaiops-serve-churn:5001" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
	if !e.called("DELETE /containers/"+servingName) || !e.called("POST /containers/"+servingName+"-id/start") {
		t.Fatalf("expected replace->create->start sequence, got %v", e.calls)
	}
	payload := e.created[servingName]
	if payload["Image"] != e.images["mlaiops-mlflow"] {
		t.Fatalf("configured default image was not used by ID: %#v", payload["Image"])
	}
	cmd := strings.Join(anyStrings(payload["Cmd"]), " ")
	if !strings.HasPrefix(cmd, "mlflow models serve -m "+modelPath+" ") {
		t.Fatalf("serving container must load the fetched local model: %s", cmd)
	}
	host := payload["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "mlaiops_default" {
		t.Fatalf("container must join the platform network: %v", host)
	}
	if strings.Join(anyStrings(host["CapDrop"]), ",") != "ALL" || strings.Join(anyStrings(host["SecurityOpt"]), ",") != "no-new-privileges" || host["PidsLimit"] != float64(defaultPidsLimit) {
		t.Fatalf("serving container is missing hardening controls: %#v", host)
	}
	if labels := payload["Labels"].(map[string]any); labels["mlaiops.artifact"] != "models:/churn-classifier/3" {
		t.Fatalf("artifact label missing: %#v", labels)
	}
}

func TestDeployUsesModelSpecificServingImage(t *testing.T) {
	e, server := newFakeEngine(t)
	e.images["ghcr.io/acme/churn-xgboost:3"] = "sha256:" + strings.Repeat("b", 64)
	manager := testManager(server.URL)
	manager.Images.AllowedPrefixes = []string{"ghcr.io/acme/"}
	manager.PidsLimit = maximumPidsLimit + 1
	if _, err := manager.Deploy(context.Background(), "churn", "models:/churn-classifier/3", " ghcr.io/acme/churn-xgboost:3 "); err != nil {
		t.Fatal(err)
	}
	payload := e.created[servingName]
	if payload["Image"] != e.images["ghcr.io/acme/churn-xgboost:3"] {
		t.Fatalf("model-specific image was not selected by ID: %#v", payload["Image"])
	}
	labels := payload["Labels"].(map[string]any)
	if labels["mlaiops.image"] != "ghcr.io/acme/churn-xgboost:3" || labels["mlaiops.image.id"] != e.images["ghcr.io/acme/churn-xgboost:3"] {
		t.Fatalf("selected image and its resolved ID were not recorded in labels: %#v", labels)
	}
	host := payload["HostConfig"].(map[string]any)
	if host["PidsLimit"] != float64(maximumPidsLimit) {
		t.Fatalf("PID limit should be capped at %d: %#v", maximumPidsLimit, host)
	}
	if fetch := e.created[servingName+"-fetch"]; fetch["Image"] != "mlaiops-mlflow" {
		t.Fatalf("the request's serving_image must never run the credentialed fetch: %#v", fetch["Image"])
	}
}

// Issue #3: model containers run untrusted code and must not get the
// platform's object-store or tracking credentials.
func TestServingContainerGetsNoCredentials(t *testing.T) {
	e, server := newFakeEngine(t)
	if _, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn-classifier/3", ""); err != nil {
		t.Fatal(err)
	}
	payload := e.created[servingName]
	if env := anyStrings(payload["Env"]); len(env) != 0 {
		t.Fatalf("serving container must get an empty environment, got %v", env)
	}
	raw := strings.Join(anyStrings(payload["Env"]), " ") + strings.Join(anyStrings(payload["Cmd"]), " ")
	for _, secret := range []string{"platform-secret", "platform-key", "AWS_", "MLFLOW_"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("serving container leaks %q: %s", secret, raw)
		}
	}
	mounts := payload["HostConfig"].(map[string]any)["Mounts"].([]any)
	mount := mounts[0].(map[string]any)
	if len(mounts) != 1 || mount["Source"] != servingName+"-model" || mount["Target"] != artifactMount || mount["ReadOnly"] != true {
		t.Fatalf("serving container must mount only its own artifact volume read-only: %#v", mounts)
	}
}

func TestFetchContainerDownloadsArtifactWithCredentials(t *testing.T) {
	e, server := newFakeEngine(t)
	if _, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn-classifier/3", ""); err != nil {
		t.Fatal(err)
	}
	fetch := e.created[servingName+"-fetch"]
	if fetch == nil {
		t.Fatalf("no fetch container was created: %v", e.calls)
	}
	if strings.Join(anyStrings(fetch["Env"]), " ") != strings.Join(fetchEnv, " ") {
		t.Fatalf("fetch container must carry the store credentials: %v", fetch["Env"])
	}
	cmd := anyStrings(fetch["Cmd"])
	if len(cmd) != 4 || cmd[0] != "python" || cmd[2] != fetchScript || cmd[3] != "models:/churn-classifier/3" {
		t.Fatalf("artifact URI must be passed as an argument to the fixed fetch script: %v", cmd)
	}
	host := fetch["HostConfig"].(map[string]any)
	if strings.Join(anyStrings(host["CapDrop"]), ",") != "ALL" || host["RestartPolicy"].(map[string]any)["Name"] != "no" {
		t.Fatalf("fetch container must be hardened and never restart: %#v", host)
	}
	if mount := host["Mounts"].([]any)[0].(map[string]any); mount["ReadOnly"] == true || mount["Source"] != servingName+"-model" {
		t.Fatalf("fetch container must write the artifact volume: %#v", mount)
	}
	if !e.called("POST /volumes/create") || !e.called("DELETE /containers/"+servingName+"-fetch-id") {
		t.Fatalf("expected a fresh volume and removal of the fetch container: %v", e.calls)
	}
}

func TestDeployFailsWhenFetchFails(t *testing.T) {
	e, server := newFakeEngine(t)
	e.script(servingName+"-fetch-id", `{"State":{"Status":"running"}}`, `{"State":{"Status":"exited","ExitCode":1}}`)
	e.logs[servingName+"-fetch-id"] = "botocore.exceptions.NoCredentialsError"
	manager := testManager(server.URL)
	manager.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := manager.Deploy(context.Background(), "churn", "models:/churn-classifier/3", "")
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") || !strings.Contains(err.Error(), "NoCredentialsError") {
		t.Fatalf("a failed fetch must fail the deploy with the fetch output: %v", err)
	}
	if e.created[servingName] != nil {
		t.Fatal("no serving container may start after a failed fetch")
	}
	if !e.called("DELETE /containers/"+servingName+"-fetch-id") || !e.called("DELETE /volumes/"+servingName+"-model") {
		t.Fatalf("a failed fetch must clean up its container and volume: %v", e.calls)
	}
}

func TestFetchTimeoutIsHonored(t *testing.T) {
	e, server := newFakeEngine(t)
	e.script(servingName+"-fetch-id", `{"State":{"Status":"running"}}`)
	manager := testManager(server.URL)
	manager.ReadyTimeout = 10 * time.Second
	clock := time.Unix(0, 0)
	manager.now = func() time.Time { return clock }
	manager.sleep = func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil }
	_, err := manager.Deploy(context.Background(), "churn", "models:/churn-classifier/3", "")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a fetch timeout, got %v", err)
	}
	if polls := e.inspects[servingName+"-fetch-id"]; polls < 10 || polls > 12 {
		t.Fatalf("expected about 10 one-second polls before the timeout, got %d", polls)
	}
}

func anyStrings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}

func TestDeployValidatesInput(t *testing.T) {
	if _, err := testManager("http://x").Deploy(context.Background(), "", "artifact", ""); err == nil {
		t.Fatal("expected validation error")
	}
	manager := testManager("http://x")
	manager.Image = ""
	if _, err := manager.Deploy(context.Background(), "churn", "artifact", ""); err == nil {
		t.Fatal("expected image configuration error")
	}
	if _, err := testManager("http://x").Deploy(context.Background(), "churn", "artifact", "   "); err == nil {
		t.Fatal("expected whitespace-only image to be rejected rather than using the fallback")
	}
}

func TestContainerNameIsStableDNSLabelForUnsafeModelName(t *testing.T) {
	first := containerName(" Customer Churn/V2 ")
	second := containerName(" Customer Churn/V2 ")
	if first != second || len(first) > 63 || !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`).MatchString(first) {
		t.Fatalf("invalid deterministic container name %q", first)
	}
	if first == containerName("Customer Churn V2") {
		t.Fatalf("different unsafe model names must not collapse to one container: %q", first)
	}
	if got := containerName("churn-classifier"); got != "mlaiops-serve-churn-classifier" {
		t.Fatalf("existing safe endpoint names should stay compatible, got %q", got)
	}
}

func TestListReadsLabels(t *testing.T) {
	_, server := newFakeEngine(t)
	deployments, err := testManager(server.URL).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 || deployments[0].Name != "churn" || deployments[0].State != "running" || deployments[0].ServingImage != "ghcr.io/acme/churn:3" {
		t.Fatalf("unexpected deployments: %+v", deployments)
	}
}

func TestPinnedAPIVersionPrefixesPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1.44/") {
			t.Fatalf("expected versioned path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	manager := testManager(server.URL)
	manager.APIVersion = "v1.44"
	if err := manager.Undeploy(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestUndeployMissingContainerIsNoError(t *testing.T) {
	e, server := newFakeEngine(t)
	if err := testManager(server.URL).Undeploy(context.Background(), "ghost"); err != nil {
		t.Fatalf("missing container must not error: %v", err)
	}
	if !e.called("DELETE /volumes/mlaiops-serve-ghost-model") {
		t.Fatalf("undeploy must remove the artifact volume: %v", e.calls)
	}
}

func TestUndeployReportsEngineErrors(t *testing.T) {
	e, server := newFakeEngine(t)
	e.on("DELETE /containers/"+servingName, http.StatusInternalServerError, `boom`)
	if err := testManager(server.URL).Undeploy(context.Background(), "churn"); err == nil {
		t.Fatal("a 500 on delete must be reported")
	}
}

func TestDeployFailsWhenCreateReturnsNotFound(t *testing.T) {
	e, server := newFakeEngine(t)
	e.on("POST /containers/create", http.StatusNotFound, `{"message":"No such image: mlaiops-mlflow:latest"}`)
	endpoint, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn/1", "")
	if err == nil || endpoint != "" {
		t.Fatalf("missing image must fail the deploy, got endpoint %q err %v", endpoint, err)
	}
	if !strings.Contains(err.Error(), "not present") || !errors.Is(err, ErrNotFound) {
		t.Fatalf("error should name the missing image: %v", err)
	}
	if e.countCalls("POST /containers/") != e.countCalls("POST /containers/create") {
		t.Fatalf("start must not be called after a failed create: %v", e.calls)
	}
}

func TestDeployFailsWhenStartReturnsNotFound(t *testing.T) {
	e, server := newFakeEngine(t)
	e.on("POST /containers/"+servingName+"-id/start", http.StatusNotFound, `{"message":"No such container"}`)
	if endpoint, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn/1", ""); err == nil || endpoint != "" {
		t.Fatalf("404 on start must fail the deploy, got endpoint %q err %v", endpoint, err)
	}
}

func TestDeployRejectsEmptyContainerID(t *testing.T) {
	e, server := newFakeEngine(t)
	e.routes["POST /containers/create"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == servingName {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + r.URL.Query().Get("name") + `-id"}`))
	}
	if _, err := testManager(server.URL).Deploy(context.Background(), "churn", "models:/churn/1", ""); err == nil {
		t.Fatal("an empty container ID must fail the deploy")
	}
	if e.called("POST /containers//start") {
		t.Fatal("start must not be called with an empty ID")
	}
}

func TestListFailsWhenEngineReturnsNotFound(t *testing.T) {
	e, server := newFakeEngine(t)
	e.on("GET /containers/json", http.StatusNotFound, `page not found`)
	if _, err := testManager(server.URL).List(context.Background()); err == nil {
		t.Fatal("List must report a 404 as an error")
	}
}

func TestDemuxLogs(t *testing.T) {
	raw := append(frame(1, "line one\n"), frame(2, "line two\n")...)
	if got := demuxLogs(raw); got != "line one\nline two\n" {
		t.Fatalf("unexpected demux %q", got)
	}
	if got := demuxLogs([]byte("plain tty output")); got != "plain tty output" {
		t.Fatalf("unframed output must pass through, got %q", got)
	}
}

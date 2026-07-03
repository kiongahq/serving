package serving

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeDocker(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/containers/"):
			w.WriteHeader(http.StatusNotFound) // nothing to replace
		case r.Method == http.MethodPost && r.URL.Path == "/containers/create":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			cmd := payload["Cmd"].([]any)
			if cmd[0] != "mlflow" || cmd[4] != "models:/churn-classifier/3" {
				t.Errorf("unexpected serve command: %v", cmd)
			}
			host := payload["HostConfig"].(map[string]any)
			if host["NetworkMode"] != "mlaiops_default" {
				t.Errorf("container must join the platform network: %v", host)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"ctr-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/containers/ctr-1/start":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/containers/json":
			_, _ = w.Write([]byte(`[{"State":"running","Labels":{"mlaiops.serving":"true","mlaiops.model":"churn","mlaiops.artifact":"models:/churn-classifier/3"}}]`))
		default:
			t.Errorf("unexpected docker call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	return server, &calls
}

func testManager(baseURL string) *Manager {
	manager := NewManager("mlaiops-mlflow", "mlaiops_default", []string{"MLFLOW_TRACKING_URI=http://mlflow:5000"})
	manager.BaseURL = baseURL
	return manager
}

func TestDeployCreatesAndStartsContainer(t *testing.T) {
	docker, calls := fakeDocker(t)
	defer docker.Close()
	endpoint, err := testManager(docker.URL).Deploy(context.Background(), "churn", "models:/churn-classifier/3")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://mlaiops-serve-churn:5001" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "DELETE") || !strings.Contains(joined, "containers/create") || !strings.Contains(joined, "start") {
		t.Fatalf("expected replace->create->start sequence, got %s", joined)
	}
}

func TestDeployValidatesInput(t *testing.T) {
	if _, err := testManager("http://x").Deploy(context.Background(), "", "artifact"); err == nil {
		t.Fatal("expected validation error")
	}
	manager := testManager("http://x")
	manager.Image = ""
	if _, err := manager.Deploy(context.Background(), "churn", "artifact"); err == nil {
		t.Fatal("expected image configuration error")
	}
}

func TestListReadsLabels(t *testing.T) {
	docker, _ := fakeDocker(t)
	defer docker.Close()
	deployments, err := testManager(docker.URL).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 || deployments[0].Name != "churn" || deployments[0].State != "running" {
		t.Fatalf("unexpected deployments: %+v", deployments)
	}
}

func TestPinnedAPIVersionPrefixesPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1.44/containers/") {
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
	docker, _ := fakeDocker(t)
	defer docker.Close()
	if err := testManager(docker.URL).Undeploy(context.Background(), "ghost"); err != nil {
		t.Fatalf("missing container must not error: %v", err)
	}
}

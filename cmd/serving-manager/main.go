// serving-manager runs real model-serving containers on the local Docker
// engine. It is the only service with access to the Docker socket, the same
// isolation posture the storage proxy has for object-store credentials.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ml-ai-ops/platform/internal/serving"
)

func main() {
	env := []string{}
	for _, key := range []string{"MLFLOW_TRACKING_URI", "MLFLOW_S3_ENDPOINT_URL", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	manager := serving.NewManager(getenv("SERVE_IMAGE", "mlaiops-mlflow"), getenv("PLATFORM_NETWORK", "mlaiops_default"), env)
	if socket := os.Getenv("DOCKER_SOCKET"); socket != "" {
		manager.SocketPath = socket
	}
	if host := os.Getenv("DOCKER_HOST_URL"); host != "" {
		manager.BaseURL = host
	}
	// Unversioned by default (daemon-latest); pin with DOCKER_API_VERSION=v1.44.
	manager.APIVersion = os.Getenv("DOCKER_API_VERSION")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /deployments", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		deployments, err := manager.List(r.Context())
		if err != nil {
			write(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		write(w, http.StatusOK, map[string]any{"deployments": deployments})
	})
	mux.HandleFunc("POST /deployments", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var request struct {
			Name        string `json:"name"`
			ArtifactURI string `json:"artifact_uri"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		endpoint, err := manager.Deploy(r.Context(), request.Name, request.ArtifactURI)
		if err != nil {
			write(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		write(w, http.StatusCreated, map[string]string{"name": request.Name, "endpoint": endpoint})
	})
	mux.HandleFunc("DELETE /deployments/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if err := manager.Undeploy(r.Context(), r.PathValue("name")); err != nil {
			write(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	server := &http.Server{Addr: ":" + getenv("PORT", "8085"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("serving-manager listening on %s (image=%s network=%s)", server.Addr, manager.Image, manager.Network)
	log.Fatal(server.ListenAndServe())
}

func authorized(r *http.Request) bool {
	token := os.Getenv("MLAIOPS_INTERNAL_TOKEN")
	return token == "" || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == token
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

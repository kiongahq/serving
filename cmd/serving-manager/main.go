// serving-manager runs real model-serving containers on the local Docker
// engine. It is the only service with access to the Docker socket, the same
// isolation posture the storage proxy has for object-store credentials.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"github.com/kiongahq/serving/internal/runtimeconfig"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kiongahq/serving/internal/serving"
)

func main() {
	if err := runtimeconfig.Load(); err != nil {
		log.Fatal(err)
	}
	if err := runtimeconfig.ValidateServing(); err != nil {
		log.Fatal(err)
	}
	env := []string{}
	for _, key := range []string{"MLFLOW_TRACKING_URI", "MLFLOW_S3_ENDPOINT_URL", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	// The store and tracking credentials go only to the trusted artifact fetch
	// container; model-serving containers never receive them.
	manager := serving.NewManager(getenv("SERVE_IMAGE", "mlaiops-mlflow"), getenv("PLATFORM_NETWORK", "mlaiops_default"), env)
	manager.FetchImage = os.Getenv("SERVE_FETCH_IMAGE")
	policy, err := imagePolicy()
	if err != nil {
		log.Fatal(err)
	}
	manager.Images = policy
	manager.PidsLimit = getenvInt64("SERVE_PIDS_LIMIT", manager.PidsLimit)
	manager.ReadyTimeout = time.Duration(getenvInt64("SERVE_READY_TIMEOUT_SECONDS", int64(serving.DefaultReadyTimeout/time.Second))) * time.Second
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
			Name         string `json:"name"`
			ArtifactURI  string `json:"artifact_uri"`
			ServingImage string `json:"serving_image,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		endpoint, err := manager.Deploy(r.Context(), request.Name, request.ArtifactURI, request.ServingImage)
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

	server := &http.Server{Addr: ":" + getenv("PORT", "8085"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: manager.ReadyTimeout + 40*time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("serving-manager listening on %s (image=%s network=%s allowed_images=%v require_digest=%t)", server.Addr, manager.Image, manager.Network, manager.Images.AllowedPrefixes, manager.Images.RequireDigest)
	log.Fatal(server.ListenAndServe())
}

// authorized fails closed: the manager starts containers through the Docker
// socket, so without MLAIOPS_INTERNAL_TOKEN it refuses every call.
func authorized(r *http.Request) bool {
	token := os.Getenv("MLAIOPS_INTERNAL_TOKEN")
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token != "" && ok && subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) == 1
}

// imagePolicy reads SERVE_IMAGE_ALLOWED_PREFIXES and SERVE_IMAGE_REQUIRE_DIGEST.
// Production always requires digest-pinned request images.
func imagePolicy() (serving.ImagePolicy, error) {
	prefixes, err := serving.ParseAllowedPrefixes(os.Getenv("SERVE_IMAGE_ALLOWED_PREFIXES"))
	if err != nil {
		return serving.ImagePolicy{}, err
	}
	production := os.Getenv("KIONGA_ENVIRONMENT") == "production"
	requireDigest := production
	if value := strings.TrimSpace(os.Getenv("SERVE_IMAGE_REQUIRE_DIGEST")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return serving.ImagePolicy{}, fmt.Errorf("SERVE_IMAGE_REQUIRE_DIGEST must be true or false")
		}
		if production && !parsed {
			return serving.ImagePolicy{}, fmt.Errorf("production requires digest-pinned serving images; remove SERVE_IMAGE_REQUIRE_DIGEST=false")
		}
		requireDigest = parsed
	}
	return serving.ImagePolicy{AllowedPrefixes: prefixes, RequireDigest: requireDigest}, nil
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

func getenvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		log.Printf("ignoring invalid %s=%q; using %d", key, value, fallback)
		return fallback
	}
	return parsed
}

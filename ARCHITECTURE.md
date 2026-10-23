# Architecture

This document describes how the serving manager is built and how it fits into
Kionga. Read it before you change how model servers start.

## Bird's-eye view

The serving manager is a small HTTP service in front of the Docker Engine API. The
platform gateway sends it a model name and an MLflow artifact URI; the manager
starts a container that runs `mlflow models serve` for that artifact on the
platform's Docker network and returns the in-network endpoint
(`http://mlaiops-serve-<name>:5001`). The gateway stores that endpoint and sends
prediction requests to it.

It has no database. The running containers are the state: each carries labels that
name the model, artifact and image, and `GET /deployments` reads them back.

## Code map

### `cmd/serving-manager`

`main.go` is the whole HTTP layer: it loads configuration, builds a
`serving.Manager`, registers the four routes (`GET /healthz`, `GET /deployments`,
`POST /deployments`, `DELETE /deployments/{name}`) on a standard library
`http.ServeMux`, and checks the bearer token. Request bodies are limited to 1 MiB
and reject unknown fields. A failed deploy returns `422`; a Docker error on list or
delete returns `502`.

### `internal/serving`

`manager.go` holds `Manager`, which talks to the Docker Engine over its Unix socket
(or `DOCKER_HOST_URL`) with plain HTTP and JSON, with no Docker SDK.

- `Deploy` removes any existing container of the same name, then creates and starts
  a new one. It uses the model's own `serving_image` when the request names one and
  the configured `SERVE_IMAGE` otherwise.
- `Undeploy` stops and removes a container; a missing container is not an error.
- `List` returns every container labelled `mlaiops.serving=true`, running or
  stopped, with its state.
- `containerName` maps a model name to a DNS-safe container name with the
  `mlaiops-serve-` prefix. A name that is not already DNS-safe, or is too long,
  gets a slug plus a short SHA-256 suffix, so two different model names never
  share a container.
- `APIVersion` optionally pins the Engine API version (`DOCKER_API_VERSION`, for
  example `v1.44`); empty means the daemon's newest.

`manager_test.go` runs every operation against a fake Engine API served by
`httptest`.

### `internal/runtimeconfig`

Loads non-secret settings from the JSON file named by `KIONGA_CONFIG_FILE` and
secrets from `<NAME>_FILE` files. It refuses secret names in the plain config file
and rejects setting both `<NAME>` and `<NAME>_FILE`. This package started as a copy
of the platform's `go/internal/runtimeconfig` (which has since gained gateway-only
production checks); keep the shared loading rules in step.

### `Dockerfile`, `Makefile`

A two-stage build: a static, stripped binary (`CGO_ENABLED=0`) from
`golang:1.26.10-alpine`, run on `gcr.io/distroless/static-debian12:nonroot`. The
`SERVICE` build argument names the command to build. `make verify`, `make build` and
`make image` are the local entry points.

## Invariants

- **Container hardening.** Every model server runs with all Linux capabilities
  dropped, `no-new-privileges`, a PID limit (`SERVE_PIDS_LIMIT`, default `256`,
  capped at `4096`) and restart policy `unless-stopped`.
- **Replace, never duplicate.** Deploying a name that already runs replaces it. This
  makes re-deploy and rollback idempotent.
- **Fail closed.** The manager returns an error when it cannot create or start the
  container, and the gateway then records the deployment as failed. It never
  returns an endpoint for a container that did not start.
- **Token check.** When `MLAIOPS_INTERNAL_TOKEN` is set, every route except
  `/healthz` requires it as a bearer token.
- **Single trusted host.** Access to the Docker socket is root-equivalent. The
  manager is the only Kionga service besides the pipeline runner that holds it.

## How it connects to other components

- **platform**: the gateway calls `POST /deployments` when a model is deployed
  (`SERVING_MANAGER_URL`) and `DELETE /deployments/{name}` on rollback, sending
  `MLAIOPS_INTERNAL_TOKEN`. Kubernetes installations use the platform operator and
  KServe instead of this service.
- **deploy**: `compose.yaml` builds this repository, mounts
  `/var/run/docker.sock`, and sets `SERVE_IMAGE=mlaiops-mlflow` (built from
  `deploy/mlflow/Dockerfile`) and `PLATFORM_NETWORK=mlaiops_default`.
- **MLflow and object storage**: the manager passes `MLFLOW_TRACKING_URI`,
  `MLFLOW_S3_ENDPOINT_URL`, `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` from its
  own environment to each model server, so `mlflow models serve` can resolve and
  download the artifact.

## Cross-cutting concerns

- **Version alignment.** The model server image must load what the pipeline runner
  and the Jupyter workbench train. The MLflow and scikit-learn pins live in
  `deploy/mlflow/Dockerfile`, `pipeline-runner/requirements.txt` and
  `workspace/jupyter/Dockerfile`.
- **Security gates.** CI runs gofmt, vet, race tests, build, govulncheck, gosec and
  gitleaks through the shared Go workflow. See [CONTRIBUTING.md](CONTRIBUTING.md).

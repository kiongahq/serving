# serving

[![CI](https://github.com/kiongahq/serving/actions/workflows/ci.yml/badge.svg)](https://github.com/kiongahq/serving/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENCE)

The Kionga model serving manager: runs one model server container per deployed model version.

## Background

Kionga is a self-hosted platform for machine learning, data and AI agents. When you
deploy a registered model from the Kionga console or API, the
[platform](https://github.com/kiongahq/platform) gateway asks this service to start
an `mlflow models serve` container for that model and records the endpoint it
returns. If the start fails, the gateway marks the deployment as failed rather than
live.

The serving manager is the model serving path for single-host installations that
run on Docker Compose. It controls containers through the Docker socket, which is
root-equivalent on the host, so run it only on a host you trust. Kubernetes
installations serve models through KServe instead.

## Install

The image is published as `ghcr.io/kiongahq/serving-manager`. To build from source
you need Go 1.26:

```bash
git clone git@github.com:kiongahq/serving.git
cd serving
make build          # bin/serving-manager
```

## Usage

The [deploy](https://github.com/kiongahq/deploy) repository runs the serving
manager with the rest of Kionga. To run it on its own against the local Docker
Engine:

```bash
export MLAIOPS_INTERNAL_TOKEN=$(openssl rand -hex 24)
SERVE_IMAGE=<an image with MLflow> ./bin/serving-manager      # listens on :8085

curl -X POST http://localhost:8085/deployments \
  -H "Authorization: Bearer $MLAIOPS_INTERNAL_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name": "churn", "artifact_uri": "models:/churn/1"}'
# {"endpoint":"http://mlaiops-serve-churn:5001","name":"churn"}
```

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness |
| `GET` | `/deployments` | List model server containers and their state |
| `POST` | `/deployments` | Start, or replace, the model server for a model |
| `DELETE` | `/deployments/{name}` | Stop and remove a model server |

`MLAIOPS_INTERNAL_TOKEN` (or `MLAIOPS_INTERNAL_TOKEN_FILE`) is required: the
manager refuses to start without it, because it drives the root-equivalent Docker
socket. Every request except `/healthz` must send it as
`Authorization: Bearer <token>`; the token is compared in constant time and a bare
token or another scheme is rejected with `401`. With `KIONGA_ENVIRONMENT=production`
the token must be at least 24 characters and must not be a development value
(containing `local` or `change-me`). The [Configuration reference](https://kiongahq.github.io/docs/reference/configuration/)
lists every setting.

## Security model

Model servers run untrusted code: loading an MLflow pyfunc or pickle model executes
Python chosen by the model's author. The manager therefore keeps platform
credentials out of every model-serving container.

- **Artifact pre-fetch.** For each deploy the manager creates a Docker volume
  (`mlaiops-serve-<name>-model`) and runs a short-lived fetch container from the
  operator's trusted image (`SERVE_FETCH_IMAGE`, default `SERVE_IMAGE`) with a
  fixed command that downloads the artifact with MLflow's resolver. That download
  only copies files; it never imports the model. The fetch container is the only
  container that receives `MLFLOW_TRACKING_URI`, `MLFLOW_S3_ENDPOINT_URL`,
  `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, and it is removed as soon as
  the download finishes or fails. A request's `serving_image` never runs this step.
- **Credential-free serving.** The model server mounts the volume read-only at
  `/opt/kionga/artifact`, serves `/opt/kionga/artifact/model`, and starts with an
  empty environment, so it cannot read the object store or the tracking server
  with platform credentials, and `docker inspect` on it shows no secret.
- **Image allowlist.** A request may pick a model-specific `serving_image` only
  from repositories in `SERVE_IMAGE_ALLOWED_PREFIXES`, a comma-separated list. An
  entry ending in `/` allows everything below it (`ghcr.io/acme/`,
  `registry.internal:5000/`); any other entry allows exactly one repository
  (`ghcr.io/acme/churn`). Docker Hub names are normalised, so `python` means
  `docker.io/library/python`. The list is empty by default: only the configured
  `SERVE_IMAGE` runs, which keeps the standard `mlaiops-mlflow` image working. A
  rejected image gets `422` and never reaches the engine.
- **Pinned digests.** With `KIONGA_ENVIRONMENT=production`, or
  `SERVE_IMAGE_REQUIRE_DIGEST=true` elsewhere, a request image must be pinned as
  `name@sha256:<digest>`. Every deploy resolves the image to its content-addressed
  ID, runs that ID, and records it in the `mlaiops.image.id` label next to the
  requested reference in `mlaiops.image`. A missing image fails the deploy.
- **What operators configure.** Give the manager store credentials that can only
  read model artifacts (for example an object-store user with read-only access to
  the `mlaiops-artifacts` and `mlaiops-models` buckets), not the store's root user.
  They are visible to anyone with Docker access while a fetch container runs.

| Variable | Default | Purpose |
| --- | --- | --- |
| `MLAIOPS_INTERNAL_TOKEN` | required | Bearer token for every route except `/healthz` |
| `SERVE_IMAGE` | `mlaiops-mlflow` | Default model server image, trusted operator configuration |
| `SERVE_IMAGE_ALLOWED_PREFIXES` | empty | Registries or repositories a request's `serving_image` may use |
| `SERVE_IMAGE_REQUIRE_DIGEST` | `true` in production, else `false` | Require `@sha256:` digests on request images; production cannot disable it |
| `SERVE_FETCH_IMAGE` | `SERVE_IMAGE` | Trusted image with MLflow and store support that downloads artifacts |
| `SERVE_READY_TIMEOUT_SECONDS` | `80` | Time a deploy may take, from download until the model is ready |
| `MLFLOW_TRACKING_URI`, `MLFLOW_S3_ENDPOINT_URL`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | unset | Passed to the fetch container only |

## Documentation

- Product documentation: [Models and serving](https://kiongahq.github.io/docs/guides/models-serving/)
- Engineering handbook: [Execution and serving](https://kiongahq.github.io/docs/engineering/components/execution/)
- How this repository is laid out: [ARCHITECTURE.md](ARCHITECTURE.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, the checks CI
runs and the commit conventions.

## License

[MIT](LICENCE) © Kionga contributors

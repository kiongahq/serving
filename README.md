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
SERVE_IMAGE=<an image with MLflow> ./bin/serving-manager      # listens on :8085

curl -X POST http://localhost:8085/deployments \
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

When `MLAIOPS_INTERNAL_TOKEN` is set, every request except `/healthz` must send it
as a bearer token. The [Configuration reference](https://kiongahq.github.io/docs/reference/configuration/)
lists every setting.

## Documentation

- Product documentation: [Models and serving](https://kiongahq.github.io/docs/guides/models-serving/)
- Engineering handbook: [Execution and serving](https://kiongahq.github.io/docs/engineering/components/execution/)
- How this repository is laid out: [ARCHITECTURE.md](ARCHITECTURE.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, the checks CI
runs and the commit conventions.

## License

[MIT](LICENCE) © Kionga contributors

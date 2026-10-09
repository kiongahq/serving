# serving

Kionga's model serving manager. When a model version is deployed from the Kionga
console or API, the [platform](https://github.com/kiongahq/platform) gateway asks this
service to start an `mlflow models serve` container for it on the platform network
and records the endpoint it returns. A rejected start is reported back as a failure,
never as a live endpoint.

Part of [Kionga](https://github.com/kiongahq): see the
[architecture overview](https://github.com/kiongahq#how-the-pieces-connect) and the
[deploy](https://github.com/kiongahq/deploy) repo for running it with everything else.

## API

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness |
| `GET` | `/deployments` | Running model servers |
| `POST` | `/deployments` | Start a model server for a model version |
| `DELETE` | `/deployments/{name}` | Stop and remove one |

Requests other than `/healthz` require `Authorization: Bearer $MLAIOPS_INTERNAL_TOKEN`
when that variable is set.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8085` | Listen port |
| `SERVE_IMAGE` | `mlaiops-mlflow` | Default model server image (a model can name its own `serving_image`) |
| `PLATFORM_NETWORK` | `mlaiops_default` | Docker network model servers join |
| `SERVE_PIDS_LIMIT` | `256` | PID limit per model server container |
| `DOCKER_SOCKET` / `DOCKER_HOST_URL` / `DOCKER_API_VERSION` | local socket | Docker Engine to use |
| `MLAIOPS_INTERNAL_TOKEN` | unset | Shared service token |

The service needs the Docker socket, which is root-equivalent on the host. Run it
only in the trusted single-host profile; on Kubernetes, serving goes through KServe
instead (see the platform operator).

## Develop

```bash
make verify          # gofmt, vet, race tests, build
make image           # ghcr.io/kiongahq/serving-manager:dev
```

CI publishes `ghcr.io/kiongahq/serving-manager` on pushes to `main` (`latest`,
`sha-<short>`) and on `v*` tags.

# Contributing to serving

Thank you for helping improve Kionga. Read [ARCHITECTURE.md](ARCHITECTURE.md) for
how the code is organized before you make a larger change.

## Development setup

Prerequisites:

- Go 1.26. `go.mod` declares `go 1.26.0` with `toolchain go1.26.10`; a newer local
  Go downloads that toolchain automatically.
- Docker with BuildKit, to build the image and to try the manager against a real
  engine.
- GNU Make.

```bash
git clone git@github.com:kiongahq/serving.git
cd serving
make verify
```

The unit tests replace the Docker Engine with an in-process HTTP server, so they do
not need Docker.

## Run the checks

CI calls the shared Go workflow
([`kiongahq/.github/.github/workflows/go.yml`](https://github.com/kiongahq/.github/blob/main/.github/workflows/go.yml)).
`make verify` runs its first four steps:

```bash
make verify      # gofmt -l (must print nothing), go vet, go test -race, go build
gofmt -w .       # fix formatting
```

Then run the security gates with the versions CI pins:

```bash
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 -quiet -exclude-dir=build -severity medium -confidence medium ./...
go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --no-banner --redact .
```

govulncheck fails on any vulnerability your code can reach, gosec on any finding of
medium severity and confidence or higher, and gitleaks on a secret anywhere in the
Git history.

## Build the image

```bash
make image                                   # ghcr.io/kiongahq/serving-manager:dev
make image IMAGE=serving-manager:local       # another tag
```

On pushes to `main` and on `v*` tags, CI builds the image with the shared image
workflow and publishes `ghcr.io/kiongahq/serving-manager` tagged `sha-<short>`,
`latest` (from `main`) and the semantic version (from tags). Pull requests build the
image without publishing it.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/), as the history
does: `type(scope): summary` in the imperative mood, for example
`fix(security): Go 1.26.10 for crypto/x509 GO-2026-6809`. Common types are `feat`,
`fix`, `docs`, `test`, `ci` and `chore`.

## Pull requests

- Keep one logical change per pull request, with tests for new behavior.
- CI must pass. Do not skip hooks or checks.
- A change to the HTTP API is a change for the platform gateway too. Open the
  matching [platform](https://github.com/kiongahq/platform) pull request and link it.

## Reviewed scanner findings

When gosec flags code that you have reviewed and found safe, annotate the line with
the rule and the reason after `--`:

```go
response, err := client.Do(request) // #nosec G704 -- host is operator config
```

If a test needs a fake credential that gitleaks reports, add the finding's
fingerprint (`commit:file:rule:line`, printed by gitleaks) to a `.gitleaksignore`
file at the repository root, with a comment that says why. A fingerprint pins one
exact finding, so any new secret still fails the scan.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENCE).

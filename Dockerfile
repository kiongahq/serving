# Local Go-service build context. Keeping it inside go/ avoids large or
# unavailable files elsewhere in a macOS iCloud-backed checkout.
FROM golang:1.25-alpine AS build
WORKDIR /src/go
COPY go.mod go.sum ./
RUN --mount=type=cache,id=kionga-go-mod,target=/go/pkg/mod go mod download
COPY . ./
ARG SERVICE=gateway
RUN --mount=type=cache,id=kionga-go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=kionga-go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /service /service
EXPOSE 8080
ENTRYPOINT ["/service"]

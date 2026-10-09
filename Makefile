IMAGE ?= ghcr.io/kiongahq/serving-manager:dev

.PHONY: test build image verify
test:
	go vet ./...
	go test -race ./...

build:
	go build -o bin/serving-manager ./cmd/serving-manager

image:
	docker build -t $(IMAGE) .

verify:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	$(MAKE) test build

.PHONY: build build-static clean test docker docker-local docker-push

BINARY_NAME=dns-prop-test
IMAGE_NAME?=dns-prop-test
REGISTRY?=
TAG?=latest

# Standard build
build:
	go build -o $(BINARY_NAME) .

# Static build for Kubernetes (no external dependencies)
build-static:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME) .

# Build for ARM64 (e.g., AWS Graviton)
build-static-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o $(BINARY_NAME)-arm64 .

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-arm64

test:
	go test -v ./...

# Docker builds using buildx bake

# Build multi-platform image (amd64 + arm64)
docker:
	docker buildx bake --file docker-bake.hcl \
		--set "*.tags=$(IMAGE_NAME):$(TAG)" \
		dns-prop-test

# Build and load locally (single platform)
docker-local:
	docker buildx bake --file docker-bake.hcl --load local

# Build and push to registry (multi-platform)
docker-push:
	docker buildx bake --file docker-bake.hcl --push \
		--set "*.tags=$(REGISTRY)/$(IMAGE_NAME):$(TAG)" \
		dns-prop-test

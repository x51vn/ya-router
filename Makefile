BINARY=github-copilot-svcs
VERSION ?= $(shell date +%Y%m%d)-local

all: build

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./src

run: build
	./$(BINARY) run

auth:
	./$(BINARY) auth

models:
	./$(BINARY) models

config:
	./$(BINARY) config

clean:
	rm -f $(BINARY)

docker-build:
	docker build --build-arg IMAGE_VERSION=$(VERSION) \
		-t $(DOCKER_REGISTRY_HOST)/dev/$(BINARY):$(VERSION) .

docker-push:
	@printf '%s' "$(DOCKER_REGISTRY_PASSWORD)" | docker login "$(DOCKER_REGISTRY_HOST)" -u "$(DOCKER_REGISTRY_USER)" --password-stdin
	docker tag $(DOCKER_REGISTRY_HOST)/dev/$(BINARY):$(VERSION) $(DOCKER_REGISTRY_HOST)/dev/$(BINARY):latest
	docker push $(DOCKER_REGISTRY_HOST)/dev/$(BINARY):$(VERSION)
	docker push $(DOCKER_REGISTRY_HOST)/dev/$(BINARY):latest

git-commit-push:
	@echo "--- git status ---"
	@git status
	git add -A
	git commit -m "chore: release $(VERSION)"
	git push

release: build docker-build docker-push git-commit-push

.PHONY: fmt vet tidy test help docker-build docker-push git-commit-push release
fmt:
	go fmt ./src/...

vet:
	go vet ./src/...

tidy:
	go mod tidy

test:
	go test ./src/...

help:
	@echo "Targets: build run auth models config clean fmt vet tidy test docker-build docker-push release"

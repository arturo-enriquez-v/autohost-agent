SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY    := autohost-agent
MAIN      := cmd/agent/main.go
PLATFORMS := linux/amd64 linux/arm64
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.Version=$(VERSION)
LAB       := scripts/lab.sh

.PHONY: help build build-all test clean release \
	lab-setup lab-create lab-delete lab-list lab-shell lab-deploy lab-update \
	lab-start lab-stop lab-restart lab-status lab-logs lab-enroll lab-link \
	node-update node-setup node-hypervisor node-status node-logs node-shell

help: ## Show this help
	@awk 'BEGIN{FS=":.*## "} /^##@/{printf "\n\033[1m%s\033[0m\n", substr($$0,5)} \
		/^[a-zA-Z0-9_%-]+:.*## /{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo -e "\nLab settings: copy lab.env.example to lab.env (NAME=, TOKEN= etc. also work on the command line)."

##@ Build

build: ## Build ./autohost-agent for this machine
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(MAIN)

build-all: ## Cross-compile linux/amd64 and linux/arm64 into dist/
	@mkdir -p dist
	@for p in $(PLATFORMS); do os=$${p%/*}; arch=$${p#*/}; out=dist/$(BINARY)-$$os-$$arch; \
		echo "  → $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o $$out $(MAIN) || exit 1; \
	done

test: ## Run the tests
	go test ./...

clean: ## Remove build output
	rm -rf $(BINARY) dist

release: ## Tag a release and push the tag; CI builds and publishes it (TAG=v1.2.3)
	@tag="$(TAG)"; \
	[ -n "$$tag" ] || read -rp "New version (latest: $$(git describe --tags --abbrev=0 2>/dev/null || echo none)): " tag; \
	[[ "$$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$ ]] || { echo "Invalid version '$$tag' (expected vX.Y.Z)"; exit 1; }; \
	[ -z "$$(git status --porcelain)" ] || { echo "Working tree is not clean"; exit 1; }; \
	git tag -a "$$tag" -m "Release $$tag" && git push origin "$$tag" && \
	echo "Pushed $$tag: GitHub Actions will build and publish the release"

##@ Lab: Incus test servers (NAME= picks the instance; otherwise you are asked)

lab-setup: ## Install and initialize Incus on this machine
	@$(LAB) setup
lab-create: ## Create a test server (NAME, RAM, CPU)
	@$(LAB) create
lab-delete: ## Delete a test server
	@$(LAB) delete
lab-list: ## List test servers
	@$(LAB) list
lab-shell: ## Open a shell in a test server
	@$(LAB) shell
lab-deploy: build ## Install the agent in a test server (creates it if needed)
	@$(LAB) deploy
lab-update: build ## Push fresh agent + CLI binaries and restart (NAME=all for every server)
	@$(LAB) update
lab-start: ## Enable and start the agent service
	@$(LAB) start
lab-stop: ## Stop the agent service
	@$(LAB) stop
lab-restart: ## Restart the agent service
	@$(LAB) restart
lab-status: ## Show the agent service status
	@$(LAB) status
lab-logs: ## Follow the agent logs
	@$(LAB) logs
lab-enroll: build ## Deploy and run the interactive `autohost up` enrollment
	@$(LAB) enroll
lab-link: build ## Deploy and link with an enrollment token (TOKEN=...)
	@$(LAB) link

##@ Lab: real node on the LAN over SSH (NODE_HOST, NODE_NAME, HOST_IP in lab.env)

node-update: build ## Copy fresh agent + CLI binaries to the node and restart
	@$(LAB) node-update
node-setup: build ## Update binaries, create an enrollment token and enroll the node
	@$(LAB) node-setup
node-hypervisor: ## Install KVM/libvirt on the node
	@$(LAB) node-hypervisor
node-status: ## Show the agent service status on the node
	@$(LAB) node-status
node-logs: ## Follow the agent logs on the node
	@$(LAB) node-logs
node-shell: ## Open a shell on the node
	@$(LAB) node-shell

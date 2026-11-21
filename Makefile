.PHONY: build web test bench lab lab-down

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build: web ## the single binary, with the UI embedded
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/bgpd ./cmd/bgpd

web:
	cd web && npm ci && npm run build

test:
	go test ./...

bench: ## injects 1.2M routes into a fresh engine
	go test ./internal/bgp -run '^$$' -bench InjectFullTable -benchtime 1x

lab: ## bgpd peering with two FRRouting routers; UI on http://127.0.0.1:8080
	VERSION=$(VERSION) docker compose up --build -d

lab-down:
	docker compose down -v

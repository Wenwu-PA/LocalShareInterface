.PHONY: build frontend test
GO ?= go

frontend:
	cd web && npm ci --no-audit --no-fund && npm run build
	touch web/dist/.gitkeep

build: frontend
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '-s -w' -o dist/lanbridge ./cmd/lanbridge

test:
	$(GO) test ./...

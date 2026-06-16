BINARY  = sonic
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -ldflags="-s -w -X 'cli.Version=$(VERSION)'"
BUILD   = CGO_ENABLED=0 go build $(LDFLAGS)

.PHONY: all build test clean run dev docker docker-run install uninstall release help

all: build

build:
	$(BUILD) -o $(BINARY) .

test:
	go test ./... -count=1 -timeout 120s -v

test-short:
	go test ./... -count=1 -timeout 30s

clean:
	rm -f $(BINARY)
	rm -rf dist/

run: build
	./$(BINARY)

dev: build
	./$(BINARY) -dev


docker:
	docker compose build

docker-run:
	docker compose up -d

docker-stop:
	docker compose down

install: build
	cp $(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed /usr/local/bin/$(BINARY)"

uninstall:
	rm -f /usr/local/bin/$(BINARY)

release: test
	@echo "Building release binaries..."
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe .
	@echo "Release binaries in dist/:"
	@ls -lh dist/

help:
	@echo "Sonic - Edge JavaScript Engine"
	@echo ""
	@echo "Usage:"
	@echo "  make build       Build sonic binary"
	@echo "  make test        Run all tests"
	@echo "  make run         Start sonic in production mode"
	@echo "  make dev         Start sonic in development mode (hot-reload)"
	@echo "  make install     Install sonic to /usr/local/bin"
	@echo "  make release     Build cross-platform release binaries"
	@echo "  make docker      Build Docker image"
	@echo "  make docker-run  Start sonic via docker compose"
	@echo "  make clean       Remove build artifacts"

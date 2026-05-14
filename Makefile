.PHONY: all build install test lint clean release

BINARY := otoxan
MAIN_PKG := ./cmd/otoxan

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILDTIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "\
	-X github.com/silas/otoxan/internal/version.Version=$(VERSION) \
	-X github.com/silas/otoxan/internal/version.Commit=$(COMMIT) \
	-X github.com/silas/otoxan/internal/version.BuildTime=$(BUILDTIME) \
"

GOOS   := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

all: build

build:
	go build $(LDFLAGS) -o bin/$(BINARY) $(MAIN_PKG)

install:
	@echo "Installing otoxan binary..."
	go install $(LDFLAGS) $(MAIN_PKG)
	@echo "Installed to $$(go env GOPATH)/bin (or GOBIN)"
	@echo "Ensure $$(go env GOPATH)/bin is on your PATH"

test:
	go test -race ./...

lint:
	go vet ./...

clean:
	rm -f $(BINARY)

release:
	@mkdir -p bin
	@echo "Building release binary..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build -trimpath \
		$(LDFLAGS) \
		-o bin/$(BINARY) $(MAIN_PKG)
	@echo "Packing tarball..."
	tar -czf bin/$(BINARY)-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz -C bin $(BINARY)
	@echo "Generating SHA256SUMS..."
	cd bin && sha256sum $(BINARY)-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz > SHA256SUMS
	@echo "Release artifacts:"
	@ls -la bin/$(BINARY)-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz bin/SHA256SUMS

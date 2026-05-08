.PHONY: all build install test lint clean

BINARY := otoxan
CMD_DIR := ./cmd/...

all: build

build:
	go build $(CMD_DIR)

install:
	@echo "Installing otoxan binaries..."
	go install $(CMD_DIR)
	@echo "Installed to $$(go env GOPATH)/bin (or GOBIN)"
	@echo "Ensure $$(go env GOPATH)/bin is on your PATH"

test:
	go test -race ./...

lint:
	go vet ./...

clean:
	rm -f $(BINARY)

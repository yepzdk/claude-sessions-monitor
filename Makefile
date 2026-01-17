.PHONY: build build-all install clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Build for current platform
build:
	go build $(LDFLAGS) -o csm .

# Build for all platforms
build-all: clean
	@mkdir -p dist
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-darwin-arm64 .
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/csm-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/csm-linux-arm64 .

# Install to ~/.local/bin
install: build
	@mkdir -p $(HOME)/.local/bin
	cp csm $(HOME)/.local/bin/csm
	@echo "Installed to $(HOME)/.local/bin/csm"

# Clean build artifacts
clean:
	rm -f csm
	rm -rf dist

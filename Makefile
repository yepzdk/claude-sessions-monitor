.PHONY: build build-all install menubar menubar-release menubar-app menubar-install packages clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# nfpm requires a version starting with a digit (deb policy), so strip any leading 'v'.
PKG_VERSION := $(patsubst v%,%,$(VERSION))

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

# Build macOS menu bar app (bundles csm binary)
menubar:
	$(MAKE) -C macos/CSMMenuBar build

menubar-release:
	$(MAKE) -C macos/CSMMenuBar build-release

# Build macOS menu bar .app bundle
menubar-app:
	$(MAKE) -C macos/CSMMenuBar app

# Build and install macOS menu bar .app bundle to /Applications
menubar-install: menubar-app
	rm -rf /Applications/CSMMenuBar.app
	cp -r macos/CSMMenuBar/.build/CSMMenuBar.app /Applications/
	xattr -d com.apple.quarantine /Applications/CSMMenuBar.app 2>/dev/null || true
	@echo "Installed CSMMenuBar.app to /Applications/"

# Build .deb and .rpm Linux packages for amd64 and arm64.
# Requires `nfpm` on PATH (see .github/workflows for the pinned version used in CI).
packages: build-all
	@command -v nfpm >/dev/null 2>&1 || { echo >&2 "nfpm not found. Install from https://nfpm.goreleaser.com/install/"; exit 1; }
	@for arch in amd64 arm64; do \
		for pkg in deb rpm; do \
			echo "Building csm $(PKG_VERSION) $$arch $$pkg"; \
			VERSION=$(PKG_VERSION) ARCH=$$arch nfpm package --config nfpm.yaml --packager $$pkg --target dist/ || exit 1; \
		done; \
	done

# Clean build artifacts
clean:
	rm -f csm
	rm -rf dist

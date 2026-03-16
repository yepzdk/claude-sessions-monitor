.PHONY: build build-release app clean

GO_ROOT := ../..
VERSION := $(shell cd $(GO_ROOT) && git describe --tags --always --dirty 2>/dev/null || echo "dev")
GOARCH := $(shell uname -m | sed 's/x86_64/amd64/')

APP_NAME := CSMMenuBar.app
APP_DIR := .build/$(APP_NAME)

build:
	cd $(GO_ROOT) && GOOS=darwin GOARCH=$(GOARCH) go build -ldflags "-X main.version=$(VERSION)" -o macos/CSMMenuBar/.build/csm .
	swift build
	cp .build/csm .build/debug/csm

build-release:
	cd $(GO_ROOT) && GOOS=darwin GOARCH=$(GOARCH) go build -ldflags "-s -w -X main.version=$(VERSION)" -o macos/CSMMenuBar/.build/csm .
	swift build -c release
	cp .build/csm .build/release/csm

app: build-release
	rm -rf $(APP_DIR)
	mkdir -p $(APP_DIR)/Contents/MacOS
	cp .build/release/CSMMenuBar $(APP_DIR)/Contents/MacOS/
	cp .build/release/csm $(APP_DIR)/Contents/MacOS/
	sed 's/1.0.0/$(VERSION)/' Sources/Info.plist > $(APP_DIR)/Contents/Info.plist
	codesign --force --sign - $(APP_DIR)

clean:
	rm -rf .build
	swift package clean

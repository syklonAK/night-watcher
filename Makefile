BIN      := night-watcher
BIN_PATH := /usr/local/bin/night-watcher
LDFLAGS  := -s -w

.PHONY: all build install uninstall clean tidy run vendor release-linux release version

all: build

tidy:
	go mod tidy

# Sync the raw VERSION file (used by the updater's mirror path) with the
# app.Version constant, so they can never drift.
version:
	@grep -oE 'Version = "[^"]+"' internal/app/app.go | grep -oE 'v[0-9.]+' > VERSION
	@echo "VERSION -> $$(cat VERSION)"

# Native Linux capture and edge modules compile without CGO.
#
# Deliberately does NOT run `tidy` first. `go mod tidy` rewrites go.mod and
# go.sum, so building used to be able to add a module checksum as a side effect
# of compiling — the one file whose whole job is to say which modules are
# expected, edited by the step that consumes it. Run `make tidy` when the
# dependencies actually change, and commit the result.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

vendor:
	go mod tidy
	go mod vendor

# Statically cross-compile Linux binaries with the native AF_PACKET backend.
release-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/night-watcher-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/night-watcher-linux-arm64 .

# GitHub release assets: night-watcher_linux_<arch>.tar.gz, each containing a single
# `night-watcher` binary. These are what install.sh and the in-app updater download.
release: version release-linux
	mkdir -p release
	cp dist/night-watcher-linux-amd64 dist/night-watcher && tar -czf release/night-watcher_linux_amd64.tar.gz -C dist night-watcher && rm dist/night-watcher
	cp dist/night-watcher-linux-arm64 dist/night-watcher && tar -czf release/night-watcher_linux_arm64.tar.gz -C dist night-watcher && rm dist/night-watcher
	@# A checksum file published beside the assets is what lets the installer and
	@# the updater prove that a mirror handed them the real binary. Users on
	@# restricted networks fetch these through third-party proxies, so this is
	@# the only integrity check they get.
	cd release && (sha256sum night-watcher_linux_*.tar.gz > SHA256SUMS 2>/dev/null || shasum -a 256 night-watcher_linux_*.tar.gz > SHA256SUMS)
	@echo "Release assets ready in ./release"
	@cat release/SHA256SUMS

install: build
	install -m 0755 $(BIN) $(BIN_PATH)
	mkdir -p /etc/night-watcher
	@echo "Installed. Run: night-watcher"

uninstall:
	rm -f $(BIN_PATH)

run: build
	sudo ./$(BIN)

clean:
	rm -f $(BIN)

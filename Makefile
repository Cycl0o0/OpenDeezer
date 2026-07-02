BIN     := opendeezer
PKG     := ./cmd/opendeezer
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

.PHONY: build run test vet tidy clean cross

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

run: build
	./$(BIN)

test:
	go test -race ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BIN) $(DIST)

# Cross-compile. Only macOS is cgo-free (oto/purego backend); every other OS
# uses malgo, which needs cgo — Windows cross-builds via mingw-w64
# (`brew install mingw-w64` / `apt install gcc-mingw-w64-x86-64`). Linux needs
# cgo+ALSA, so it's built natively in CI (.github/workflows/release.yml).
cross:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-darwin-arm64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-darwin-amd64  $(PKG)
	@command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1 || \
		{ echo "error: windows needs cgo (malgo audio); install mingw-w64 for x86_64-w64-mingw32-gcc" >&2; exit 1; }
	CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS) -extldflags=-static" -o $(DIST)/$(BIN)-windows-amd64.exe $(PKG)
	@echo "built: $(DIST)/"

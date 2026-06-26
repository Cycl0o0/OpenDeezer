# Build the macOS SwiftUI app. `make app` produces OpenDeezer.app; `make run` opens it.
ROOT    := ../..
CONFIG  := release
APP     := OpenDeezer.app
BIN     := OpenDeezer
BUILDIR := .build/$(CONFIG)
# Match the SDK so the Go archive and Swift link target agree.
export MACOSX_DEPLOYMENT_TARGET := 26.0

.PHONY: all corelib build app run clean

all: app

# Compile the Go engine to a universal (arm64 + x86_64) C static archive so the
# app runs on both Apple Silicon and Intel Macs.
corelib:
	cd $(ROOT) && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -buildmode=c-archive \
		-o gui/macos/Clib/libdeezercore-arm64.a ./corelib
	cd $(ROOT) && CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -buildmode=c-archive \
		-o gui/macos/Clib/libdeezercore-amd64.a ./corelib
	lipo -create Clib/libdeezercore-arm64.a Clib/libdeezercore-amd64.a \
		-output Clib/libdeezercore.a
	cp Clib/libdeezercore-arm64.h Clib/libdeezercore.h

build: corelib
	swift build -c $(CONFIG) --arch arm64 --arch x86_64

# Assemble a double-clickable universal .app bundle.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp Info.plist $(APP)/Contents/Info.plist
	cp .build/apple/Products/$(CONFIG)/$(BIN) $(APP)/Contents/MacOS/$(BIN)
	cp $(ROOT)/assets/OpenDeezer.icns $(APP)/Contents/Resources/OpenDeezer.icns
	@echo "built $(APP) (universal)"

run: app
	open $(APP)

clean:
	rm -rf .build $(APP) Clib/libdeezercore.a Clib/libdeezercore.h

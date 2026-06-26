# Build the macOS SwiftUI app. `make app` produces Deezer.app; `make run` opens it.
ROOT    := ../..
CONFIG  := release
APP     := Deezer.app
BIN     := DeezerGUI
BUILDIR := .build/$(CONFIG)
# Match the SDK so the Go archive and Swift link target agree.
export MACOSX_DEPLOYMENT_TARGET := 26.0

.PHONY: all corelib build app run clean

all: app

# Compile the Go engine to a C static archive + header into Clib/.
corelib:
	cd $(ROOT) && CGO_ENABLED=1 go build -buildmode=c-archive \
		-o gui/macos/Clib/libdeezercore.a ./corelib

build: corelib
	swift build -c $(CONFIG)

# Assemble a double-clickable .app bundle.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp Info.plist $(APP)/Contents/Info.plist
	cp $(BUILDIR)/$(BIN) $(APP)/Contents/MacOS/$(BIN)
	@echo "built $(APP)"

run: app
	open $(APP)

clean:
	rm -rf .build $(APP) Clib/libdeezercore.a Clib/libdeezercore.h

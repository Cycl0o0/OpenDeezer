# OpenDeezer (Windows)

Native **WinUI 3 / C++/WinRT / Fluent** front-end for OpenDeezer — *an open
source reimplementation of Deezer*. The whole engine (login, browse, Blowfish
decrypt, MP3 decode, WASAPI playback) is the Go core compiled to a C-ABI shared
library (`lib/libdeezercore.dll`) and called in-process over `extern "C"`; this
layer is UI only.

## Look

- **Fluent + Mica:** a `NavigationView` shell (Liked Songs / Playlists / Search)
  with a Mica backdrop, a track `ListView`, album-art `GridView`s and a bottom
  now-playing transport bar.
- **Deezer purple (`#A238FF`):** set as the `SystemAccentColor` override and on
  the play button + seek/volume sliders.
- **Code-only UI:** the entire tree is built in C++ in the window constructor —
  no `.xaml`, no `.idl`, no XAML markup compiler. The one XAML concession is the
  `App` implementing `IXamlMetadataProvider` so default control themes resolve.

## Build & run

```powershell
gui\windows\build.ps1
.\gui\windows\bin\x64\Release\OpenDeezer.exe
```

`build.ps1` (1) builds the Go DLL with MinGW
(`CGO_ENABLED=1 GOOS=windows go build -buildmode=c-shared -o gui/windows/lib/libdeezercore.dll ./corelib`),
(2) generates the MSVC import lib from `libdeezercore.def` via `lib.exe`,
(3) runs `msbuild /restore /p:Configuration=Release /p:Platform=x64`, and
(4) copies the DLL next to the exe.

### Prerequisites

- **Go 1.24+** on PATH.
- **MinGW-w64 gcc** (`x86_64-w64-mingw32-gcc`) on PATH — `c-shared` needs cgo.
  Get it from [WinLibs](https://winlibs.com/) or MSYS2 (`pacman -S mingw-w64-x86_64-gcc`).
  Override the compiler with `$env:CC` if your binary has a different name.
- **Visual Studio 2022** with *Desktop development with C++* (MSBuild, `lib.exe`,
  v143 toolset) and a recent **Windows 11 SDK** (10.0.22621+). NuGet restore pulls
  the Windows App SDK 1.6 + CppWinRT packages automatically.

The app is **unpackaged** and **self-contained** (`WindowsPackageType=None`,
`WindowsAppSDKSelfContained=true`): the Windows App SDK framework DLLs are copied
into the output folder, so it runs with no separate runtime install. The Go DLL
is built `-extldflags=-static`, so `libdeezercore.dll` ships alone (no
`libwinpthread`/`libgcc`).

## ARL

Login reads the ARL from `%DEEZER_ARL%`, or `%APPDATA%\opendeezer\arl.txt`. Your
ARL is the `arl` cookie from an authenticated `deezer.com` session — treat it like
a password.

## Architecture

```
gui/windows/
  src/main.cpp          App + MainWindow built entirely in code; DZ* extern "C";
                        JSON via Windows.Data.Json; threading via resume_background +
                        DispatcherQueue; cover art via BitmapImage from bytes;
                        300 ms DispatcherQueueTimer poll + DZFinishedCount auto-advance
  OpenDeezer.vcxproj    unpackaged x64 WinUI 3 project (no markup compiler)
  app.manifest          per-monitor-v2 DPI awareness
  libdeezercore.def     DZ* exports -> MSVC import lib
  build.ps1             Go DLL -> import lib -> msbuild -> copy DLL
  lib/                  generated libdeezercore.{dll,lib,h} (git-ignored)
```

The C API is defined in `../../corelib/deezercore.go`
(`go build -buildmode=c-shared`). Every blocking `DZ*` call runs on a worker via
`winrt::resume_background` and the result is marshalled back to the UI thread with
`winrt::resume_foreground(Window.DispatcherQueue())`. Cheap state reads
(`DZState`/`DZPositionMS`/`DZDurationMS`/`DZFinishedCount`) plus `DZSeek` /
`DZSetVolume` / `DZTogglePause` run inline from the poll tick.

## License

AGPL-3.0. By Cycl0o0.

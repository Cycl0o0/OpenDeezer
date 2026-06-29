# OpenDeezer (Windows)

Native **WinUI 3 / C# (.NET 8) / Fluent** front-end for OpenDeezer — *an open
source reimplementation of Deezer*. The whole engine (login, browse, Blowfish
decrypt, MP3/FLAC decode, WASAPI playback) is the Go core compiled to a C-ABI
shared library (`lib/libdeezercore.dll`) and called in-process via **P/Invoke**;
this layer is UI only.

## Look

- **Fluent + Mica:** a `NavigationView` shell (Liked Songs / Flow / Playlists /
  Charts / Podcasts / Search) with a Mica backdrop, a track `ListView`, album-art
  `GridView`s, an Artist/Lyrics view, and a bottom now-playing transport bar.
- **Deezer purple (`#A238FF`):** an `AccentBrush` resource used on the play button
  + seek/volume sliders, the lyric highlight, and the Connect indicator.
- **Real XAML markup compiler:** `App.xaml` is compiled and merges
  `XamlControlsResources`, so the WinUI theme dictionary resolves and the app
  launches. (The previous code-only C++/WinRT build never merged it, so the first
  theme-brush lookup threw `0x80004005` at startup.) The window content tree is
  built in code-behind (`MainWindow.xaml.cs`), a direct port of the old C++ UI.

## Build & run

```powershell
gui\windows\build.ps1
```

`build.ps1` (1) builds the Go DLL with MinGW
(`CGO_ENABLED=1 GOOS=windows go build -buildmode=c-shared -o gui/windows/lib/libdeezercore.dll ./corelib`),
(2) runs `dotnet publish OpenDeezer.csproj -c Release -r win-x64 --self-contained -p:WindowsPackageType=None`,
and (3) copies the DLL next to `OpenDeezer.exe` in the publish dir. The script
prints the final exe path (under `bin\x64\Release\net8.0-windows10.0.19041.0\win-x64\publish\`).

### Prerequisites

- **.NET 8 SDK** on PATH (`dotnet`).
- **Go 1.24+** on PATH.
- **MinGW-w64 gcc** (`x86_64-w64-mingw32-gcc`) on PATH — `c-shared` needs cgo.
  Get it from [WinLibs](https://winlibs.com/) or MSYS2 (`pacman -S mingw-w64-x86_64-gcc`).
  Override the compiler with `$env:CC` if your binary has a different name.

No Visual Studio / MSBuild / import-lib step is required: P/Invoke loads
`libdeezercore.dll` by name at runtime. The app is **unpackaged** and
**self-contained** (`WindowsPackageType=None`, `WindowsAppSDKSelfContained=true`):
the Windows App SDK framework DLLs are published into the output folder, so it
runs with no separate runtime install. The Go DLL is built `-extldflags=-static`,
so `libdeezercore.dll` ships alone (no `libwinpthread`/`libgcc`).

## Login

On first launch the app shows a **"Log in with Deezer"** chooser. Picking it opens
an embedded **WebView2** (`Microsoft.UI.Xaml.Controls.WebView2`) pointed at the
Deezer web login; once you sign in, OpenDeezer polls the `CoreWebView2`
cookie store (`CookieManager.GetCookiesAsync`) for the `arl` cookie — which is
`HttpOnly`, so it is only readable there, not via `document.cookie` — captures it
automatically, starts the session with `DZInit`, and writes it to
`%APPDATA%\opendeezer\arl.txt` so the next launch logs in automatically. No manual
ARL copy needed.

This needs the **Microsoft Edge WebView2 Runtime** (the Evergreen runtime ships
with Windows 11 / current Edge and is preinstalled on the CI runner). The WebView2
projection ships transitively with the Windows App SDK — no separate NuGet
package.

### Manual ARL (fallback)

You can still log in by ARL directly: choose **"Enter ARL manually"** in the
chooser, or pre-seed `%DEEZER_ARL%` / `%APPDATA%\opendeezer\arl.txt` before launch.
Your ARL is the `arl` cookie from an authenticated `deezer.com` session — treat it
like a password.

## Architecture

```
gui/windows/
  App.xaml / App.xaml.cs        Application + the XamlControlsResources merge (the launch fix)
  MainWindow.xaml               Window shell: Mica backdrop + empty root Grid
  MainWindow.xaml.cs            full UI tree built in code + all logic (nav, playback,
                                poll, login, SMTC, tray, Connect, settings)
  DeezerCore.cs                 P/Invoke to libdeezercore.dll (DZ* exports) + marshaling helpers
  Models.cs                     wire structs + System.Text.Json parsers + config/settings I/O
  Interop.cs                    Win32 tray (Shell_NotifyIcon) + SMTC GetForWindow interop
  OpenDeezer.csproj             unpackaged, self-contained C# WinUI 3 (.NET 8) project
  app.manifest                  per-monitor-v2 DPI awareness, asInvoker
  build.ps1                     Go DLL -> dotnet publish -> copy DLL
  lib/                          generated libdeezercore.dll (git-ignored)
```

The C API is defined in `../../corelib/deezercore.go`
(`go build -buildmode=c-shared`). Every blocking `DZ*` call runs on the thread pool
via `await Task.Run(...)`; because the callers start on the UI thread (which carries
the `DispatcherQueueSynchronizationContext`), the code after each `await` resumes
back on the UI thread. Cheap state reads (`DZState`/`DZPositionMS`/`DZDurationMS`/
`DZFinishedCount`) plus `DZSeek` / `DZSetVolume` / `DZTogglePause` run inline from a
300 ms `DispatcherQueueTimer` poll, which also auto-advances the queue when
`DZFinishedCount()` increments.

Char\* results from the engine are UTF-8 and freed with `DZFree` (see
`DeezerCore.TakeJson`); cover-art bytes from `DZFetch` are freed with `DZFreeBytes`
(see `DeezerCore.Fetch`).

## License

AGPL-3.0. By Cycl0o0.

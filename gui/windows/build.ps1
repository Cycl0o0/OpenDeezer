#requires -Version 5.1
# Build the OpenDeezer Windows app end-to-end:
#   1. compile the Go engine to a C-ABI shared library (lib\libdeezercore.dll) with MinGW
#   2. generate the MSVC import lib from libdeezercore.def via lib.exe
#   3. msbuild /restore the WinUI 3 app (Release|x64)
#   4. copy libdeezercore.dll next to OpenDeezer.exe
#
# Usage (from a normal or a "Developer PowerShell for VS 2022" prompt):
#   gui\windows\build.ps1
# Prereqs: Go 1.24+, MinGW-w64 gcc (x86_64-w64-mingw32-gcc) on PATH, VS2022 with
# "Desktop development with C++", and the Windows App SDK / Windows 11 SDK.
[CmdletBinding()]
param([string]$Configuration = 'Release')
$ErrorActionPreference = 'Stop'

$here   = $PSScriptRoot
$root   = (Resolve-Path (Join-Path $here '..\..')).Path   # repo root: module github.com/Cycl0o0/OpenDeezer
$libDir = Join-Path $here 'lib'
New-Item -ItemType Directory -Force -Path $libDir | Out-Null

Write-Host '==> [1/4] building Go c-shared DLL (lib\libdeezercore.dll)' -ForegroundColor Cyan
# CGO is required (c-shared needs cgo); on Windows oto/v3 uses WASAPI via purego,
# but the c-shared runtime still links through MinGW. -extldflags=-static folds in
# libwinpthread/libgcc so only libdeezercore.dll has to ship.
if (-not $env:CC) { $env:CC = 'x86_64-w64-mingw32-gcc' }
$env:CGO_ENABLED = '1'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
Push-Location $root
try {
  & go build -buildmode=c-shared -trimpath -ldflags '-s -w -extldflags=-static' -o 'gui/windows/lib/libdeezercore.dll' ./corelib
  if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
} finally { Pop-Location }

# Ensure the MSVC toolchain (lib.exe / msbuild) is on PATH; enter a VS dev shell if not.
function Enter-VsDev {
  $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
  if (-not (Test-Path $vswhere)) { return $false }
  $vs = & $vswhere -latest -products * -requires Microsoft.Component.MSBuild -property installationPath
  if (-not $vs) { return $false }
  $mod = Join-Path $vs 'Common7\Tools\Microsoft.VisualStudio.DevShell.dll'
  if (-not (Test-Path $mod)) { return $false }
  Import-Module $mod
  Enter-VsDevShell -VsInstallPath $vs -SkipAutomaticLocation -DevCmdArguments '-arch=x64 -host_arch=x64' | Out-Null
  return $true
}
if (-not (Get-Command msbuild -ErrorAction SilentlyContinue) -or -not (Get-Command lib -ErrorAction SilentlyContinue)) {
  Write-Host '==> entering Visual Studio developer shell' -ForegroundColor Cyan
  if (-not (Enter-VsDev)) { throw 'Visual Studio not found. Run this from a "Developer PowerShell for VS 2022" prompt.' }
}

Write-Host '==> [2/4] generating MSVC import lib (lib\libdeezercore.lib)' -ForegroundColor Cyan
& lib /nologo "/def:$(Join-Path $here 'libdeezercore.def')" /machine:x64 "/out:$(Join-Path $libDir 'libdeezercore.lib')"
if ($LASTEXITCODE -ne 0) { throw "lib.exe failed ($LASTEXITCODE)" }

Write-Host '==> [3/4] building WinUI 3 app (msbuild /restore)' -ForegroundColor Cyan
& msbuild (Join-Path $here 'OpenDeezer.vcxproj') /restore /m /p:Configuration=$Configuration /p:Platform=x64
if ($LASTEXITCODE -ne 0) { throw "msbuild failed ($LASTEXITCODE)" }

Write-Host '==> [4/4] copying libdeezercore.dll next to the exe' -ForegroundColor Cyan
$outDir = Join-Path $here "bin\x64\$Configuration"
Copy-Item (Join-Path $libDir 'libdeezercore.dll') $outDir -Force
Write-Host "==> done -> $(Join-Path $outDir 'OpenDeezer.exe')" -ForegroundColor Green

#requires -Version 5.1
# Build the OpenDeezer Windows app end-to-end:
#   1. compile the Go engine to a C-ABI shared library (lib\libdeezercore.dll) with MinGW
#   2. dotnet publish the C# WinUI 3 app (self-contained, win-x64)
#   3. copy libdeezercore.dll next to OpenDeezer.exe in the publish dir
#
# P/Invoke loads libdeezercore.dll by name at runtime, so NO MSVC import lib
# (lib.exe) and NO msbuild/VS toolchain are needed -- `dotnet` (preinstalled on
# the runner and the user's box) drives the whole .NET build.
#
# Usage:  gui\windows\build.ps1
# Prereqs: Go 1.24+, MinGW-w64 gcc (x86_64-w64-mingw32-gcc) on PATH, .NET 8 SDK.
[CmdletBinding()]
param([string]$Configuration = 'Release')
$ErrorActionPreference = 'Stop'

$here   = $PSScriptRoot
$root   = (Resolve-Path (Join-Path $here '..\..')).Path   # repo root: module github.com/Cycl0o0/OpenDeezer
$libDir = Join-Path $here 'lib'
New-Item -ItemType Directory -Force -Path $libDir | Out-Null

Write-Host '==> [1/3] building Go c-shared DLL (lib\libdeezercore.dll)' -ForegroundColor Cyan
# CGO is required (c-shared needs cgo); -extldflags=-static folds in
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

Write-Host '==> [2/3] publishing C# WinUI 3 app (dotnet publish)' -ForegroundColor Cyan
& dotnet publish (Join-Path $here 'OpenDeezer.csproj') -c $Configuration -r win-x64 --self-contained -p:WindowsPackageType=None
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed ($LASTEXITCODE)" }

Write-Host '==> [3/3] copying libdeezercore.dll into the publish dir' -ForegroundColor Cyan
# Resolve the publish dir robustly: find OpenDeezer.exe under bin and use its folder.
$exe = Get-ChildItem -Path (Join-Path $here 'bin') -Filter 'OpenDeezer.exe' -Recurse -File -ErrorAction SilentlyContinue |
       Where-Object { $_.FullName -match '\\publish\\' } |
       Select-Object -First 1
if (-not $exe) {
  $exe = Get-ChildItem -Path (Join-Path $here 'bin') -Filter 'OpenDeezer.exe' -Recurse -File -ErrorAction SilentlyContinue |
         Select-Object -First 1
}
if (-not $exe) { throw 'OpenDeezer.exe not found under bin after publish.' }
$outDir = $exe.Directory.FullName
Copy-Item (Join-Path $libDir 'libdeezercore.dll') $outDir -Force

Write-Host "==> done -> $(Join-Path $outDir 'OpenDeezer.exe')" -ForegroundColor Green

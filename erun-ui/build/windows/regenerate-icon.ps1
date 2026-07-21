<#
regenerate-icon.ps1 - Regenerate the Windows app-icon assets from appicon.png.

The macOS build (erun-ui/build.sh) bakes build/appicon.png into an .icns bundle
at build time via sips/iconutil. Windows has no equivalent step: neither the
Scoop installer (bucket/erun.json), build.ps1, nor build.sh's windows branch run
`wails build`, so plain `go build` embeds no icon unless a *.syso resource sits in
the erun-ui package dir. Go auto-links `*_windows_<arch>.syso` on the matching
Windows target and ignores it everywhere else, so the committed sysos give every
Windows build path the icon for free with no script or installer changes.

The icon group MUST live at resource ID 3: the Wails runtime loads the window /
taskbar icon from that exact ID (winc.AppIconID = 3), and `wails build` embeds it
there via winres.SetIcon(winres.RT_ICON, ...) (RT_ICON == 3). A generic tool like
rsrc numbers the group differently, which makes Explorer's file icon work but
leaves the running app's taskbar generic. So we reproduce wails' own embedding
with the same library (github.com/tc-hib/winres, pinned to the version wails uses).

Regenerates, from build/appicon.png:
  build/windows/icon.ico            - multi-resolution icon (also used by wails build)
  ../../rsrc_windows_amd64.syso     - COFF resource linked into windows/amd64 builds
  ../../rsrc_windows_arm64.syso     - COFF resource linked into windows/arm64 builds

Run it (from anywhere) whenever build/appicon.png changes, then commit the results.

Requires:
  magick  (ImageMagick) - scoop install imagemagick
  go                    - scoop install go   (winres is fetched into the module cache)
#>
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

# winres version must match the one wails v2 vendors, so the emitted resource is
# byte-for-byte what `wails build` would produce. Bump this in lockstep with the
# wails upgrade (see erun-ui/go.mod -> github.com/wailsapp/wails/v2).
$WinresVersion = "v0.3.1"

# Resolve the toolchain even from a shell with a stale in-memory PATH (mirrors build.ps1).
$persisted = ([Environment]::GetEnvironmentVariable('Path','Machine'), [Environment]::GetEnvironmentVariable('Path','User')) -join ';'
$env:Path = $persisted + ';' + $env:Path + ';' + (Join-Path $env:USERPROFILE 'go\bin')

function Require-Cmd($name, $hint) {
    $cmd = Get-Command $name -ErrorAction SilentlyContinue
    if (-not $cmd) { throw "'$name' not found on PATH. $hint" }
    return $cmd
}
Require-Cmd go "Install with: scoop install go" | Out-Null
$magickCmd = Require-Cmd magick "Install with: scoop install imagemagick"

# The Scoop (portable) ImageMagick build has no registry keys, so magick.exe
# can't locate its coder DLLs when invoked via a shim. Point it at its own
# install dir explicitly. Resolve the real exe behind any Scoop shim, then set
# the module/config paths and put the dir on PATH so dependent CORE_RL_*.dll load.
$magickExe = $magickCmd.Source
$shimFile = [IO.Path]::ChangeExtension($magickExe, ".shim")
if (Test-Path $shimFile) {
    $line = (Get-Content $shimFile | Where-Object { $_ -match '^\s*path\s*=' } | Select-Object -First 1)
    if ($line) { $magickExe = ($line -replace '^\s*path\s*=\s*', '').Trim().Trim('"') }
}
$magickDir = Split-Path $magickExe -Parent
if (Test-Path (Join-Path $magickDir "modules\coders")) {
    $env:MAGICK_CODER_MODULE_PATH = Join-Path $magickDir "modules\coders"
    $env:MAGICK_CONFIGURE_PATH = $magickDir
    $env:Path = $magickDir + ';' + $env:Path
}

$WindowsDir = $PSScriptRoot                      # erun-ui/build/windows
$BuildDir   = Split-Path $WindowsDir -Parent     # erun-ui/build
$UiDir      = Split-Path $BuildDir -Parent       # erun-ui
$AppIcon    = Join-Path $BuildDir "appicon.png"
$IcoPath    = Join-Path $WindowsDir "icon.ico"

if (-not (Test-Path $AppIcon)) { throw "source icon not found: $AppIcon" }

Write-Host ">> icon.ico from appicon.png ..."
& $magickExe $AppIcon -define icon:auto-resize=256,128,96,64,48,32,16 $IcoPath
if ($LASTEXITCODE -ne 0) { throw "magick failed to build icon.ico" }

# Emit the sysos with a throwaway Go program that uses tc-hib/winres exactly as
# wails build does (icon group at winres.RT_ICON == 3). Kept out of the repo so
# the app module stays free of a build-only dependency.
$gen = Join-Path ([IO.Path]::GetTempPath()) ("erun-genicon-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $gen | Out-Null
try {
    @'
package main

import (
	"fmt"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	f, err := os.Open(os.Args[1])
	must(err)
	defer f.Close()
	ico, err := winres.LoadICO(f)
	must(err)
	rs := winres.ResourceSet{}
	must(rs.SetIcon(winres.RT_ICON, ico))
	for name, arch := range map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64} {
		out := fmt.Sprintf("%s/rsrc_windows_%s.syso", os.Args[2], name)
		fo, err := os.Create(out)
		must(err)
		must(rs.WriteObject(fo, arch))
		must(fo.Close())
		fmt.Println("wrote", out)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
'@ | Set-Content -Path (Join-Path $gen "main.go") -Encoding UTF8

    Push-Location $gen
    try {
        go mod init erun-genicon 2>&1 | Out-Null
        go get "github.com/tc-hib/winres@$WinresVersion"
        if ($LASTEXITCODE -ne 0) { throw "failed to fetch winres $WinresVersion" }
        Write-Host ">> rsrc_windows_{amd64,arm64}.syso via winres $WinresVersion ..."
        go run . $IcoPath $UiDir
        if ($LASTEXITCODE -ne 0) { throw "winres generator failed" }
    } finally { Pop-Location }
} finally {
    Remove-Item -Recurse -Force $gen -ErrorAction SilentlyContinue
}

Write-Host ">> ok - icon.ico and rsrc_windows_*.syso regenerated" -ForegroundColor Green

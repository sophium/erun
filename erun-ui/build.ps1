<#
build.ps1 - Windows counterpart of erun-ui/build.sh.

Builds the ERun desktop binary (erun-app.exe): installs the go.mod-pinned Wails
CLI, generates frontend bindings, builds the React/Vite frontend with Yarn, then
compiles the Go desktop app with the desktop,production tags and -H windowsgui.

Unlike build.sh this skips the lint/typecheck/format gates for a fast "produce
the binary" build (run those via the pre-commit hook or `yarn` directly). The
Wails Windows build uses CGO, so a C compiler (MinGW gcc) must be on PATH.

  .\build.ps1                         # -> erun-ui\bin\erun-app.exe
  .\build.ps1 -Target C:\path\app.exe # explicit output (run.ps1 uses erun-cli\bin)
#>
[CmdletBinding()]
param(
    [string]$Target
)

$ErrorActionPreference = "Stop"

# Make the toolchain (go/node/yarn/gcc, Scoop-installed and persisted to PATH)
# resolvable even if the launching shell has a stale in-memory PATH. Dedup the
# result: blindly appending compounds an already-long inherited PATH (the nested
# app -> run.ps1 -> PTY-tab launch chain each prepend), and once PATH passes
# cmd.exe's ~8 KB limit, yarn's spawned cmd child truncates it and drops node
# ("'node' is not recognized"), silently skipping the frontend build.
$persisted = ([Environment]::GetEnvironmentVariable('Path','Machine'), [Environment]::GetEnvironmentVariable('Path','User')) -join ';'
$env:Path = (($persisted + ';' + $env:Path) -split ';' | Where-Object { $_ -ne '' } | Select-Object -Unique) -join ';'

$ScriptDir = $PSScriptRoot
$RepoRoot = Split-Path $ScriptDir -Parent
if (-not $Target) { $Target = Join-Path $ScriptDir "bin\erun-app.exe" }
New-Item -ItemType Directory -Force -Path (Split-Path $Target -Parent) | Out-Null

function Require-Cmd($name, $hint) {
    if (-not (Get-Command $name -ErrorAction SilentlyContinue)) { throw "'$name' not found on PATH. $hint" }
}
Require-Cmd go   "Install with: scoop install go"
Require-Cmd node "Install with: scoop install nodejs"
Require-Cmd yarn "Install with: scoop install yarn"
Require-Cmd gcc  "erun-app.exe needs a C compiler. Install with: scoop install mingw"

# --- version stamp (mirror build.sh) --------------------------------------
$versionFile = Join-Path $RepoRoot "erun-devops\VERSION"
$buildVersion = if (Test-Path $versionFile) { (Get-Content $versionFile -Raw).Trim() } else { "dev" }
$buildCommit = ""
$buildDate = ""
if (git -C $ScriptDir rev-parse --is-inside-work-tree 2>$null) {
    $buildCommit = (git -C $ScriptDir rev-parse --short=12 HEAD).Trim()
    $buildDate = (git -C $ScriptDir show -s --format=%cI HEAD).Trim()
}

Push-Location $ScriptDir
try {
    # --- Wails CLI (pinned to go.mod), cached in bin\.wails-bin -------------
    $wailsVersion = (go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
    if ($LASTEXITCODE -ne 0) { throw "failed to resolve Wails version" }
    $wailsBin = Join-Path $ScriptDir "bin\.wails-bin"
    $wailsExe = Join-Path $wailsBin "wails.exe"
    if (-not (Test-Path $wailsExe)) {
        Write-Host ">> installing pinned Wails CLI ($wailsVersion) ..."
        New-Item -ItemType Directory -Force -Path $wailsBin | Out-Null
        $env:GOBIN = $wailsBin
        try {
            go install "github.com/wailsapp/wails/v2/cmd/wails@$wailsVersion"
            if ($LASTEXITCODE -ne 0) { throw "failed to install Wails CLI" }
        } finally { Remove-Item Env:GOBIN -ErrorAction SilentlyContinue }
    }

    Write-Host ">> generating frontend bindings ..."
    & $wailsExe generate module
    if ($LASTEXITCODE -ne 0) { throw "failed to generate frontend bindings" }

    # --- frontend ----------------------------------------------------------
    Push-Location (Join-Path $ScriptDir "frontend")
    try {
        Write-Host ">> yarn install ..."
        if (Test-Path "yarn.lock") { yarn install --frozen-lockfile } else { yarn install }
        if ($LASTEXITCODE -ne 0) { throw "failed to install frontend dependencies" }
        Write-Host ">> yarn build ..."
        yarn build
        if ($LASTEXITCODE -ne 0) { throw "failed to build frontend assets" }
    } finally { Pop-Location }

    # --- desktop binary ----------------------------------------------------
    Write-Host ">> building $([System.IO.Path]::GetFileName($Target)) ($buildVersion) ..."
    $ldflags = "-s -w -H windowsgui " +
               "-X github.com/sophium/erun/erun-ui.buildVersion=$buildVersion " +
               "-X github.com/sophium/erun/erun-ui.buildCommit=$buildCommit " +
               "-X github.com/sophium/erun/erun-ui.buildDate=$buildDate"
    go build -trimpath -tags "desktop,production" -ldflags $ldflags -o $Target .
    if ($LASTEXITCODE -ne 0) { throw "failed to build erun-app.exe" }
} finally { Pop-Location }

Write-Host ">> ok -> $Target" -ForegroundColor Green

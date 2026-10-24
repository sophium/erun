<#
build.ps1 - Windows counterpart of erun-ui/build.sh.

Builds the ERun desktop binary (erun-app.exe): generates frontend bindings with
the go.mod-pinned Wails CLI (`go tool wails`), builds the React/Vite frontend
with Yarn, then compiles the Go desktop app with the desktop,production tags
and -H windowsgui.

Like build.sh it runs no lint/typecheck/format gates (the component's test
stage does; run them via the pre-commit hook or `yarn` directly). The Wails
Windows build uses CGO, so a C compiler (MinGW gcc) must be on PATH.

  .\build.ps1                         # -> erun-ui\bin\erun-app.exe
  .\build.ps1 -Target C:\path\app.exe # explicit output (run.ps1 uses erun-cli\bin)
#>
[CmdletBinding()]
param(
    [string]$Target
)

$ErrorActionPreference = "Stop"

# Re-resolve the persisted PATH (the shell's may be stale) and dedup it: past
# cmd.exe's ~8 KB limit yarn's child drops node and skips the frontend build.
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
    Write-Host ">> generating frontend bindings ..."
    # The generator compiles this package, whose production assets embed
    # frontend\dist, so the directory has to exist before the bundle does.
    New-Item -ItemType Directory -Force -Path (Join-Path $ScriptDir "frontend\dist") | Out-Null
    go tool wails generate module
    if ($LASTEXITCODE -ne 0) { throw "failed to generate frontend bindings" }

    Push-Location (Join-Path $ScriptDir "frontend")
    try {
        Write-Host ">> yarn install ..."
        if (Test-Path "yarn.lock") { yarn install --frozen-lockfile } else { yarn install }
        if ($LASTEXITCODE -ne 0) { throw "failed to install frontend dependencies" }
        Write-Host ">> yarn build ..."
        yarn build
        if ($LASTEXITCODE -ne 0) { throw "failed to build frontend assets" }
    } finally { Pop-Location }

    Write-Host ">> building $([System.IO.Path]::GetFileName($Target)) ($buildVersion) ..."
    # Single-quoted so a checkout path with spaces survives -ldflags splitting.
    # `main.`, not the module path: -X silently ignores a path no package declares.
    $skillsSource = Join-Path $RepoRoot "erun-skills\skills"
    $agentsSource = Join-Path $RepoRoot "erun-skills\agents"
    $ldflags = "-s -w -H windowsgui " +
               "-X main.buildVersion=$buildVersion " +
               "-X main.buildCommit=$buildCommit " +
               "-X main.buildDate=$buildDate " +
               "-X 'main.buildSkillsSource=$skillsSource' " +
               "-X 'main.buildAgentsSource=$agentsSource'"
    go build -trimpath -tags "desktop,production" -ldflags $ldflags -o $Target .
    if ($LASTEXITCODE -ne 0) { throw "failed to build erun-app.exe" }
} finally { Pop-Location }

Write-Host ">> ok -> $Target" -ForegroundColor Green

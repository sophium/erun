<#
run.ps1 - Windows counterpart of erun-cli/run.sh.

Rebuilds the erun CLI from source on every invocation and execs it, so `erun`
always runs the latest source. For `erun app` it also rebuilds the desktop app
(via erun-ui/build.ps1) into erun-cli\bin\erun-app.exe before launching, so
`erun app` resolves the fresh desktop next to erun.exe (erun-cli/cmd/app.go).

Invoked via the `erun` shim (erun.cmd) on PATH; not usually called directly.
#>
[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$CliArgs
)

$ErrorActionPreference = "Stop"

# Make the toolchain resolvable even from a shell with a stale in-memory PATH.
$persisted = ([Environment]::GetEnvironmentVariable('Path','Machine'), [Environment]::GetEnvironmentVariable('Path','User')) -join ';'
$env:Path = $persisted + ';' + $env:Path

$ScriptDir = $PSScriptRoot                       # erun-cli
$RepoRoot = Split-Path $ScriptDir -Parent
$Bin = Join-Path $ScriptDir "bin"
$CliExe = Join-Path $Bin "erun.exe"
$AppExe = Join-Path $Bin "erun-app.exe"
$UiBuild = Join-Path $RepoRoot "erun-ui\build.ps1"
New-Item -ItemType Directory -Force -Path $Bin | Out-Null

# --- version stamp (mirror run.sh) ----------------------------------------
$versionFile = Join-Path $RepoRoot "erun-devops\VERSION"
$buildVersion = if (Test-Path $versionFile) { (Get-Content $versionFile -Raw).Trim() } else { "dev" }
$buildCommit = ""
$buildDate = ""
if (git -C $ScriptDir rev-parse --is-inside-work-tree 2>$null) {
    $buildCommit = (git -C $ScriptDir rev-parse --short=12 HEAD).Trim()
    $buildDate = (git -C $ScriptDir show -s --format=%cI HEAD).Trim()
}

# --- detect subcommand (first non-flag arg) and --no-shell quiet mode ------
$commandName = ""
foreach ($a in $CliArgs) {
    if ($a -eq "--") { break }
    if ($a -notlike "-*") { $commandName = $a; break }
}
$quiet = ($CliArgs -contains "--no-shell")

# --- always rebuild the CLI (CGO disabled; fast) ---------------------------
$ldflags = "-X github.com/sophium/erun/cmd.buildVersion=$buildVersion " +
           "-X github.com/sophium/erun/cmd.buildCommit=$buildCommit " +
           "-X github.com/sophium/erun/cmd.buildDate=$buildDate"
if (-not $quiet) { [Console]::Error.Write(">> rebuilding erun CLI ($buildVersion)... ") }
$env:CGO_ENABLED = "0"
try {
    Push-Location $ScriptDir
    go build -ldflags $ldflags -o $CliExe .
    if ($LASTEXITCODE -ne 0) { throw "failed to build erun.exe" }
    Pop-Location
} finally { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }
if (-not $quiet) { [Console]::Error.WriteLine("ok -> $CliExe") }

# --- for `erun app`, rebuild the desktop bundle into the same bin ----------
# Non-fatal like run.sh: a missing wails/yarn/node/gcc toolchain warns but still
# lets the CLI run (its `app` subcommand emits a clear not-found message).
if ($commandName -eq "app") {
    try {
        & $UiBuild -Target $AppExe
        if ($LASTEXITCODE -ne 0 -and $null -ne $LASTEXITCODE) { throw "build.ps1 returned $LASTEXITCODE" }
    } catch {
        [Console]::Error.WriteLine(">> skipping desktop rebuild: $($_.Exception.Message) (missing wails/yarn/node/gcc toolchain?)")
    }
}

# Prepend bin so the launched desktop app (and any PTY tab it spawns) resolves
# the freshly-built plain erun.exe here, not the rebuilding shim on PATH.
$env:Path = "$Bin;$env:Path"

& $CliExe @CliArgs
exit $LASTEXITCODE

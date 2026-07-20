<#
run.ps1 - Windows counterpart of erun-mcp/run.sh.

Rebuilds the emcp (ERun MCP server) binary from source on every invocation and
execs it, so `emcp` always runs the latest source. Invoked via the `emcp` shim
(emcp.cmd) on PATH; not usually called directly.
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

$ScriptDir = $PSScriptRoot                       # erun-mcp
$RepoRoot = Split-Path $ScriptDir -Parent
$Bin = Join-Path $ScriptDir "bin"
$McpExe = Join-Path $Bin "emcp.exe"
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

$ldflags = "-X github.com/sophium/erun/erun-mcp.buildVersion=$buildVersion " +
           "-X github.com/sophium/erun/erun-mcp.buildCommit=$buildCommit " +
           "-X github.com/sophium/erun/erun-mcp.buildDate=$buildDate"
$env:CGO_ENABLED = "0"
try {
    Push-Location $ScriptDir
    go build -ldflags $ldflags -o $McpExe ./cmd/emcp
    if ($LASTEXITCODE -ne 0) { throw "failed to build emcp.exe" }
    Pop-Location
} finally { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }

& $McpExe @CliArgs
exit $LASTEXITCODE

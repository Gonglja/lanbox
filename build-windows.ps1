<#
.SYNOPSIS
  Build the lanbox Windows executables.

.DESCRIPTION
  Cross-compiles (or natively builds) the lanbox server, CLI and desktop agent
  for Windows amd64 / arm64 into dist\, using the same file names as `make dist`.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File build-windows.ps1
  powershell -ExecutionPolicy Bypass -File build-windows.ps1 -Version v1.2.3 -Arch amd64
#>
[CmdletBinding()]
param(
  [string]$Version = "dev",
  [ValidateSet("amd64", "arm64", "all")]
  [string]$Arch = "all",
  [string]$OutDir = "dist",
  [switch]$NoTrim,
  [switch]$NoAgent
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

if ($Version -eq "dev") {
  $described = & git describe --tags --always --dirty 2>$null
  if ($LASTEXITCODE -eq 0 -and $described) { $Version = $described.Trim() }
}

$ldflags = "-s -w -X main.version=$Version"
$arches = if ($Arch -eq "all") { @("amd64", "arm64") } else { @($Arch) }
$targets = @(
  @{ Name = "lanbox"; Pkg = "./cmd/lanbox"; Tags = ""; LdFlags = $ldflags },
  @{ Name = "lanbox-cli"; Pkg = "./cmd/lanbox-cli"; Tags = ""; LdFlags = $ldflags }
)
if (-not $NoAgent) {
  # Wails GUI: needs the desktop,production tags and -H windowsgui (no console).
  $targets += @{ Name = "lanbox-agent"; Pkg = "./cmd/lanbox-agent"; Tags = "desktop,production"; LdFlags = "$ldflags -H=windowsgui" }
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# Cross-compilation uses environment variables; prefixing "VAR=x cmd" is a POSIX
# shell feature that cmd.exe does not support.
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$goFlags = @()
if (-not $NoTrim) { $goFlags += "-trimpath" }

$built = @()
foreach ($a in $arches) {
  $env:GOARCH = $a
  foreach ($t in $targets) {
    $out = Join-Path $OutDir ("{0}-windows-{1}.exe" -f $t.Name, $a)
    Write-Host "==> $out  (windows/$a)"
    $tags = @()
    if ($t.Tags) { $tags = @("-tags", $t.Tags) }
    & go build @tags @goFlags -ldflags $t.LdFlags -o $out $t.Pkg
    if ($LASTEXITCODE -ne 0) { throw "build failed: $out" }
    $built += Get-Item $out
  }
}

Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "built $($built.Count) artifacts (version $Version):"
$built | ForEach-Object {
  Write-Host ("  {0,-34} {1,10:N0} bytes" -f $_.Name, $_.Length)
}

# Checksums, same format as `make checksums`.
$sumFile = Join-Path $OutDir "SHA256SUMS"
$lines = $built | Sort-Object Name | ForEach-Object {
  $h = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
  "$h  $($_.Name)"
}
Set-Content -Path $sumFile -Value $lines -Encoding ascii
Write-Host ""
Write-Host "checksums written to $sumFile"

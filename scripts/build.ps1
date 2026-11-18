[CmdletBinding()]
param(
    [ValidateSet('Core', 'Client', 'Check', 'UIBuild', 'Release')]
    [string]$Task = 'Check',
    [string]$CoreRef = 'v0.1.0-beta.1'
)

$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$corePath = Join-Path $repoRoot '.core'

function Invoke-Tool {
    param([string]$Program, [string[]]$Arguments, [string]$Directory = $repoRoot)
    Push-Location -LiteralPath $Directory
    try {
        & $Program @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$Program exited with code $LASTEXITCODE" }
    } finally { Pop-Location }
}

function Initialize-Core {
    if (-not (Test-Path -LiteralPath (Join-Path $corePath '.git'))) {
        Invoke-Tool git @('-c', 'advice.detachedHead=false', 'clone', '--depth', '1', '--branch', $CoreRef,
            'https://github.com/prusa3d/prusa-connect.git', $corePath)
    }
    foreach ($package in @('typescript', 'svelte')) {
        $packagePath = Join-Path $corePath "packages/$package"
        $outputPath = if ($package -eq 'typescript') { 'dist/index.js' } else { 'dist/build/module.js' }
        if (-not (Test-Path -LiteralPath (Join-Path $packagePath $outputPath))) {
            Invoke-Tool pnpm @('install', '--frozen-lockfile') $packagePath
            Invoke-Tool pnpm @('run', 'build') $packagePath
        }
    }
}

function Invoke-ClientGenerator {
    param([switch]$Check)
    $generatorArgs = @('-C', $corePath, 'run', './cmd/generate-client-sdk', '-module',
        '-document', (Join-Path $repoRoot 'openapi.json'), '-namespace',
        'com.github.aux.octoeverywhere.connect-extension', '-name', 'octoeverywhere-client',
        '-out', (Join-Path $repoRoot 'client'))
    if ($Check) { $generatorArgs += '-check' }
    Invoke-Tool go $generatorArgs
}

function Initialize-Client {
    Invoke-Tool pnpm @('install', '--frozen-lockfile') (Join-Path $repoRoot 'client')
}

function Build-UI {
    Invoke-Tool pnpm @('run', 'build') (Join-Path $repoRoot 'client')
    Invoke-Tool pnpm @('install', '--frozen-lockfile') (Join-Path $repoRoot 'ui')
    Invoke-Tool pnpm @('run', 'build') (Join-Path $repoRoot 'ui')
}

Initialize-Core
if ($Task -eq 'Core') { return }
if ($Task -eq 'Client') {
    Invoke-ClientGenerator
    Initialize-Client
    Invoke-Tool pnpm @('run', 'generate:types') (Join-Path $repoRoot 'client')
    Invoke-Tool pnpm @('run', 'build') (Join-Path $repoRoot 'client')
    return
}

Initialize-Client
Invoke-Tool go @('-C', $corePath, 'run', (Join-Path $repoRoot 'scripts/check-manifest.go'), $repoRoot)
Invoke-ClientGenerator -Check
Invoke-Tool node @('scripts/check-client.mjs')
if ($Task -eq 'Check') {
    Invoke-Tool go @('vet', './...')
    Invoke-Tool go @('test', './...')
    Invoke-Tool pnpm @('run', 'build') (Join-Path $repoRoot 'client')
    Invoke-Tool pnpm @('install', '--frozen-lockfile') (Join-Path $repoRoot 'ui')
    Invoke-Tool pnpm @('run', 'check') (Join-Path $repoRoot 'ui')
    Invoke-Tool pnpm @('run', 'build') (Join-Path $repoRoot 'ui')
    return
}

Build-UI
if ($Task -eq 'UIBuild') { return }

$releasePath = [IO.Path]::GetFullPath((Join-Path $repoRoot 'release'))
if ($releasePath -ne (Join-Path $repoRoot 'release') -or
    [IO.Path]::GetDirectoryName($releasePath) -ne $repoRoot) {
    throw 'Refusing to replace a release directory outside this repository.'
}
if (Test-Path -LiteralPath $releasePath) { Remove-Item -LiteralPath $releasePath -Recurse -Force }
Copy-Item -LiteralPath (Join-Path $repoRoot 'ui/dist') -Destination $releasePath -Recurse
# Match the marketplace allowlist. OpenAPI is embedded in the program;
# developer documentation stays in the source repository.
foreach ($file in @('LICENSE', 'README.md', 'CHANGELOG.md')) {
    Copy-Item -LiteralPath (Join-Path $repoRoot $file) -Destination $releasePath
}

$manifestLines = Get-Content -LiteralPath (Join-Path $repoRoot 'module.yaml')
$moduleName = (($manifestLines | Where-Object { $_ -match '^name:' } | Select-Object -First 1) -replace '^name:\s*', '').Trim()
$platforms = @()
$inPlatforms = $false
foreach ($line in $manifestLines) {
    if ($line -match '^platforms:') { $inPlatforms = $true; continue }
    if ($inPlatforms -and $line -match '^\S') { $inPlatforms = $false }
    if ($inPlatforms -and $line -match '^\s+-\s+(\w+_\w+)') { $platforms += $Matches[1] }
}
if (-not $moduleName -or $platforms.Count -eq 0) { throw 'Manifest name or platforms are missing.' }

$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    foreach ($platform in $platforms) {
        $env:GOOS, $env:GOARCH = $platform.Split('_', 2)
        $binary = "${moduleName}_${platform}"
        if ($env:GOOS -eq 'windows') { $binary += '.exe' }
        Write-Host "Building $binary"
        Invoke-Tool go @('build', '-trimpath', '-ldflags=-s -w', '-o', (Join-Path $releasePath $binary), '.')
    }
} finally {
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:CGO_ENABLED = $oldCGO
}
Write-Host "Release assembled in $releasePath. Nothing has been published."

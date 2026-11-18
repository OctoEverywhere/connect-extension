#Requires -Version 7.2
<#
.SYNOPSIS
Runs the real Prusa Connect host with simulated printers and cameras on this PC.
.EXAMPLE
pwsh -NoProfile -File scripts/local-demo.ps1
.EXAMPLE
pwsh -NoProfile -File scripts/local-demo.ps1 Start -Rebuild
.EXAMPLE
pwsh -NoProfile -File scripts/local-demo.ps1 Stop
.EXAMPLE
pwsh -NoProfile -File scripts/local-demo.ps1 GrantPause
#>
[CmdletBinding()]
param(
    [ValidateSet('Start', 'Stop', 'Status', 'GrantPause')]
    [string]$Task = 'Start',
    [switch]$Rebuild,
    [string]$PrinterId
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'This launcher uses Windows process management. Run it with PowerShell 7 on Windows.' }
if ($Rebuild -and $Task -ne 'Start') { throw '-Rebuild is only valid with Start.' }
if ($PSBoundParameters.ContainsKey('PrinterId') -and ($Task -ne 'GrantPause' -or [string]::IsNullOrWhiteSpace($PrinterId))) {
    throw '-PrinterId must name one simulated printer and is only valid with GrantPause.'
}

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$corePath = Join-Path $repoRoot '.core'
$demoRoot = Join-Path $repoRoot 'bin/local-demo'
$toolsPath = Join-Path $demoRoot 'tools'
$dataPath = Join-Path $demoRoot 'data'
$demoExe = Join-Path $toolsPath 'connect-demo.exe'
$cliExe = Join-Path $toolsPath 'connect.exe'
$processFile = Join-Path $demoRoot 'process.json'
$passwordFile = Join-Path $demoRoot 'password.txt'
$cameraFile = Join-Path $demoRoot 'camera-credential.json'
$operatorFile = Join-Path $demoRoot 'operator.yaml'
$configFile = Join-Path $dataPath 'config.yaml'
$settingsFile = Join-Path $demoRoot 'extension-settings.json'
$installedFile = Join-Path $demoRoot 'installed.json'
$stdoutFile = Join-Path $demoRoot 'server.stdout.log'
$stderrFile = Join-Path $demoRoot 'server.stderr.log'
$releasePath = Join-Path $repoRoot 'release'
$baseURL = 'http://127.0.0.1:18480'
$modulePath = 'github.com/octoeverywhere/connect-extension'
$moduleAPI = "$baseURL/api/v1/modules/com.github.aux.octoeverywhere.connect-extension"
$pageURL = "$baseURL/m/com.github.aux.octoeverywhere.connect-extension/octoeverywhere"

function Invoke-Tool {
    param([string]$Program, [string[]]$Arguments, [string]$Directory = $repoRoot)
    Push-Location -LiteralPath $Directory
    try {
        & $Program @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$Program exited with code $LASTEXITCODE" }
    } finally { Pop-Location }
}

function Write-JSON {
    param([string]$Path, [object]$Value)
    [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 12), [Text.UTF8Encoding]::new($false))
}

function Get-ReleaseFingerprint {
    # Include locales, manifest, API contract and supporting assets, not only
    # index.js: rebuilding a translation must also update the installed bundle.
    $parts = foreach ($file in Get-ChildItem -LiteralPath $releasePath -File -Recurse | Sort-Object FullName) {
        [IO.Path]::GetRelativePath($releasePath, $file.FullName) + ':' + (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    }
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($parts -join "`n"))))
}

function Repair-MovedState {
    param([string]$PreviousRoot)
    if ($PreviousRoot -eq $repoRoot) { return }
    if (-not [IO.Path]::IsPathFullyQualified($PreviousRoot)) { throw 'The previous demo checkout path is invalid.' }
    # These are our own cached files in the NEW checkout. Never follow or write
    # the previous paths. Rebase before either the installer or server reads
    # them, so a copied checkout cannot launch an old checkout's executable.
    foreach ($path in @($configFile, (Join-Path $dataPath 'resources.json'))) {
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $content = [IO.File]::ReadAllText($path)
        foreach ($pair in @(
            @($PreviousRoot.Replace('\', '\\'), $repoRoot.Replace('\', '\\')),
            @($PreviousRoot.Replace('\', '/'), $repoRoot.Replace('\', '/')),
            @($PreviousRoot, $repoRoot)
        )) {
            $content = $content.Replace($pair[0], $pair[1], [StringComparison]::OrdinalIgnoreCase)
        }
        [IO.File]::WriteAllText($path, $content, [Text.UTF8Encoding]::new($false))
    }
    Write-Host 'Updated this local demo state for the renamed checkout.'
}

function Read-OwnedProcess {
    if (-not (Test-Path -LiteralPath $processFile)) { return $null }
    $held = Get-Content -LiteralPath $processFile -Raw | ConvertFrom-Json
    if ($held.repo -ne $repoRoot -or $held.executable -ne $demoExe) {
        throw "The process record does not belong to this checkout. Refusing to use it: $processFile"
    }
    $running = Get-Process -Id ([int]$held.pid) -ErrorAction SilentlyContinue
    if ($null -eq $running) {
        Remove-Item -LiteralPath $processFile -Force
        return $null
    }
    if ($running.Path -ne $demoExe -or $running.StartTime.ToUniversalTime().Ticks.ToString() -ne $held.start_ticks) {
        throw 'The saved process ID now belongs to another process. Refusing to stop or reuse it.'
    }
    return $running
}

function Stop-OwnedProcess {
    $running = Read-OwnedProcess
    if ($null -eq $running) { return }
    # Kill only the process whose path and creation time match our record, plus
    # its module children. Windows locks a running extension executable.
    $running.Kill($true)
    if (-not $running.WaitForExit(10000)) { throw 'The local demo did not stop within ten seconds.' }
    Remove-Item -LiteralPath $processFile -Force
}

function Assert-PortFree {
    $probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 18480)
    try { $probe.Start() }
    catch { throw "Port 18480 is already in use. This launcher will not stop or replace an unowned server. Stop the other server before starting this demo." }
    finally { $probe.Stop() }
}

function Wait-LocalHTTP {
    param([Diagnostics.Process]$Process)
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    while ([DateTime]::UtcNow -lt $deadline) {
        $Process.Refresh()
        if ($Process.HasExited) { throw "The demo exited. Read $stderrFile and $stdoutFile." }
        try {
            $response = Invoke-WebRequest -Uri "$baseURL/api/v1/health" -TimeoutSec 2 -SkipHttpErrorCheck
            if ([int]$response.StatusCode -ge 200 -and [int]$response.StatusCode -lt 300) { return }
        } catch { }
        Start-Sleep -Milliseconds 300
    }
    throw "The local demo did not become ready. Read $stderrFile and $stdoutFile."
}

function Start-OwnedProcess {
    Assert-PortFree
    $password = [IO.File]::ReadAllText($passwordFile).Trim()
    # Every value is generated or a local path. Windows paths cannot contain a
    # quotation mark; quote all arguments to support checkout paths with spaces.
    $arguments = @('-addr', '127.0.0.1:18480', '-dir', $dataPath, '-config', $operatorFile,
        '-examples', (Join-Path $demoRoot 'unused-examples'), '-printers', '3', '-flap', '0',
        '-password', $password, '-log-level', 'error', '-cameras=true')
    $quoted = ($arguments | ForEach-Object { '"' + $_ + '"' }) -join ' '
    $running = Start-Process -FilePath $demoExe -ArgumentList $quoted -WorkingDirectory $repoRoot `
        -WindowStyle Hidden -RedirectStandardOutput $stdoutFile -RedirectStandardError $stderrFile -PassThru
    Write-JSON $processFile @{ repo = $repoRoot; executable = $demoExe; pid = $running.Id; start_ticks = $running.StartTime.ToUniversalTime().Ticks.ToString() }
    try { Wait-LocalHTTP $running }
    catch { Stop-OwnedProcess; throw }
    return $running
}

function Build-DemoHost {
    Write-Host 'Building the local Connect host and its web app. The first build downloads dependencies.'
    Invoke-Tool pwsh @('-NoProfile', '-File', (Join-Path $PSScriptRoot 'build.ps1'), 'Core')
    foreach ($package in @('packages/fractal/client', 'frontend')) {
        $packagePath = Join-Path $corePath $package
        Invoke-Tool pnpm @('install', '--frozen-lockfile') $packagePath
        Invoke-Tool pnpm @('run', 'build') $packagePath
    }
    $source = Join-Path $corePath 'frontend/dist'
    if (-not (Test-Path -LiteralPath (Join-Path $source 'index.html'))) { throw 'The Connect frontend build did not create index.html.' }
    $embed = [IO.Path]::GetFullPath((Join-Path $corePath 'internal/transport/http/httpapi/appdist'))
    $expected = [IO.Path]::GetFullPath((Join-Path $repoRoot '.core/internal/transport/http/httpapi/appdist'))
    if ($embed -ne $expected -or -not $embed.StartsWith($corePath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Refusing to replace an app embed directory outside this checkout.'
    }
    if (Test-Path -LiteralPath $embed) { Remove-Item -LiteralPath $embed -Recurse -Force }
    Copy-Item -LiteralPath $source -Destination $embed -Recurse
    Invoke-Tool go @('run', './cmd/compress-app', $embed) $corePath
    [IO.File]::WriteAllText((Join-Path $embed '.gitkeep'), '')
    Invoke-Tool go @('build', '-tags', 'full diagnostics', '-ldflags=-X main.version=0.1.0-beta.1', '-o', $demoExe, './cmd/demo') $corePath
    Invoke-Tool go @('build', '-tags', 'full', '-ldflags=-X main.version=0.1.0-beta.1', '-o', $cliExe, './cmd/prusa-connect') $corePath
}

function Initialize-CameraAccess {
    $password = [IO.File]::ReadAllText($passwordFile).Trim()
    $login = @{ username = 'demo'; password = $password } | ConvertTo-Json
    $null = Invoke-RestMethod -Uri "$baseURL/api/v1/auth/login" -Method Post -ContentType 'application/json' -Body $login -SessionVariable session
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    $status = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        try {
            $status = Invoke-RestMethod -Uri "$moduleAPI/status" -WebSession $session -TimeoutSec 5
            if ($status.printers.Count -ge 4) { break }
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    if ($null -eq $status -or $status.printers.Count -lt 4) { throw "The four simulated printers did not become ready. Read $stderrFile and open Connect's extension diagnostics." }

    $credential = $null
    if (Test-Path -LiteralPath $cameraFile) {
        $candidate = Get-Content -LiteralPath $cameraFile -Raw | ConvertFrom-Json
        try {
            $null = Invoke-RestMethod -Uri "$baseURL/api/v1/cameras" -Headers @{ Authorization = 'Bearer ' + $candidate.token } -TimeoutSec 5
            $credential = $candidate
        } catch { }
    }
    if ($null -eq $credential) {
        $body = @{ name = 'OctoEverywhere local simulator camera access'; scopes = @('read') } | ConvertTo-Json
        $credential = Invoke-RestMethod -Uri "$baseURL/api/v1/auth/credentials" -Method Post -WebSession $session -ContentType 'application/json' -Body $body
        Write-JSON $cameraFile $credential
    }
    $body = @{ url = $baseURL; token = $credential.token } | ConvertTo-Json
    $null = Invoke-RestMethod -Uri "$moduleAPI/settings/connect" -Method Post -WebSession $session -ContentType 'application/json' -Body $body
    # The demo creates cameras after registering its complete fleet. A printer
    # becoming visible is not evidence that its camera has been declared yet.
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    do {
        $status = Invoke-RestMethod -Uri "$moduleAPI/status" -WebSession $session -TimeoutSec 5
        $missingCameras = @($status.printers | Where-Object { $_.cameras.Count -eq 0 })
        if ($status.printers.Count -ge 4 -and $missingCameras.Count -eq 0) { break }
        Start-Sleep -Milliseconds 400
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($status.printers.Count -lt 4 -or $missingCameras.Count -gt 0) {
        throw 'The simulated cameras did not become available. Camera access was saved; retry Start after stopping the demo.'
    }
    foreach ($printer in $status.printers) {
        if ($printer.camera_id -eq '' -and $printer.cameras.Count -gt 0) {
            $body = @{ enabled = $false; camera_id = $printer.cameras[0].id; auto_pause = $false;
                warning_confidence = 3; pause_confidence = 3; warmup_seconds = 180 } | ConvertTo-Json
            $null = Invoke-RestMethod -Uri "$moduleAPI/printers/$($printer.id)" -Method Put -WebSession $session -ContentType 'application/json' -Body $body
        }
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    $snapshotOK = $false
    do {
        try {
            $tested = Invoke-RestMethod -Uri "$moduleAPI/printers/$($status.printers[0].id)/test-camera" -Method Post -WebSession $session -ContentType 'application/json' -Body '{}' -TimeoutSec 5
            if ($tested.ok) { $snapshotOK = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 400
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $snapshotOK) { throw 'Camera selection was saved, but a fresh test snapshot could not be read. Check the camera on the extension page.' }
    Write-Host "Camera access is ready for $($status.printers.Count) simulated printers."
    Write-Host 'A fresh JPEG passed the extension camera test. This made no Gadget API request.'
    if ($status.configured) {
        Write-Host 'Your saved Gadget key and existing monitoring settings were preserved. Enabled monitoring may upload simulated camera frames.' -ForegroundColor Yellow
    } else {
        Write-Host 'No Gadget API key is configured. No snapshots will be sent to Gadget.'
    }
}

function Grant-DemoPauseAccess {
    # The task dispatch has verified the PID, executable and start time against
    # this checkout's process record. Never use this helper with a hardware host.
    $password = [IO.File]::ReadAllText($passwordFile).Trim()
    $login = @{ username = 'demo'; password = $password } | ConvertTo-Json
    $null = Invoke-RestMethod -Uri "$baseURL/api/v1/auth/login" -Method Post -ContentType 'application/json' -Body $login -SessionVariable session -TimeoutSec 10
    $fleet = Invoke-RestMethod -Uri "$baseURL/api/v1/devices" -WebSession $session -TimeoutSec 10

    # Only the four serials started by this runner qualify. Do not infer that
    # every device admitted to an otherwise local demo is a simulated printer.
    $demoSerials = @('DEMO-01', 'DEMO-02', 'DEMO-03', 'DEMO-06')
    $printers = @($fleet.items | Where-Object {
        $_.kind -eq 'printer' -and $_.connected -and
        $_.PSObject.Properties['serial'] -and $_.serial -cin $demoSerials
    })
    if (-not [string]::IsNullOrEmpty($PrinterId)) {
        $printers = @($printers | Where-Object { $_.id -ceq $PrinterId })
        if ($printers.Count -ne 1) {
            throw "Printer '$PrinterId' is not a connected simulated printer owned by this demo. No permissions were changed."
        }
    }
    if ($printers.Count -eq 0) { throw 'No connected simulated printers were found. No permissions were changed.' }

    $body = @{ relation = 'operator'; subject = 'sink:octoeverywhere' } | ConvertTo-Json
    foreach ($printer in $printers) {
        $deviceId = [Uri]::EscapeDataString($printer.id)
        $grant = Invoke-RestMethod -Uri "$baseURL/api/v1/grants/device/$deviceId" -Method Post -ContentType 'application/json' -Body $body -WebSession $session -TimeoutSec 10
        if ($grant.object -ne "device:$($printer.id)" -or $grant.relation -ne 'operator' -or $grant.subject -ne 'sink:octoeverywhere') {
            throw "Connect returned an unexpected grant for simulated printer '$($printer.serial)'."
        }
        Write-Host "Automatic-pause permission is ready for $($printer.serial) ($($printer.id))."
    }
    Write-Host 'Return to OctoEverywhere and save your printer settings. Monitoring and automatic pausing have not been enabled by this command.'
}

function Show-Connection {
    Write-Host ''
    Write-Host 'Local simulator: no physical printer is connected by this launcher.'
    Write-Host "Open       $pageURL"
    Write-Host 'Username   demo'
    Write-Host "Password   $([IO.File]::ReadAllText($passwordFile).Trim())"
    Write-Host "State/logs $demoRoot"
    Write-Host 'Stop       pwsh -NoProfile -File scripts/local-demo.ps1 Stop'
    Write-Host 'Rebuild    pwsh -NoProfile -File scripts/local-demo.ps1 Start -Rebuild'
    Write-Host 'Pause grant pwsh -NoProfile -File scripts/local-demo.ps1 GrantPause'
}

$existing = Read-OwnedProcess
if ($Task -eq 'Stop') {
    Stop-OwnedProcess
    Write-Host 'Local simulator stopped. Its settings and test data are preserved.'
    return
}
if ($Task -eq 'Status') {
    if ($null -eq $existing) { Write-Host 'Local simulator is stopped.' }
    else { Write-Host "Local simulator is running (PID $($existing.Id))."; Show-Connection }
    return
}
if ($Task -eq 'GrantPause') {
    if ($null -eq $existing) { throw "Start this checkout's simulator before granting pause access: pwsh -NoProfile -File scripts/local-demo.ps1 Start" }
    Grant-DemoPauseAccess
    return
}
if ($null -ne $existing -and -not $Rebuild) {
    Write-Host 'Local simulator is already running. Use -Rebuild after changing extension source.'
    Show-Connection
    return
}
if ($null -ne $existing) { Stop-OwnedProcess }
Assert-PortFree

foreach ($directory in @($demoRoot, $toolsPath, $dataPath)) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
if (-not (Test-Path -LiteralPath $passwordFile)) {
    [IO.File]::WriteAllText($passwordFile, [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(18)), [Text.UTF8Encoding]::new($false))
}
$releaseYAML = $releasePath.Replace('\', '/').Replace("'", "''")
$databaseYAML = (Join-Path $dataPath 'unused-records.db').Replace('\', '/').Replace("'", "''")
@"
core:
  modules_local:
    github.com/octoeverywhere/connect-extension: '$releaseYAML'
resources:
  db:
    records:
      type: sqlite
      enabled: false
      config:
        path: '$databaseYAML'
"@ | Set-Content -LiteralPath $operatorFile -Encoding utf8NoBOM
Write-JSON $settingsFile @{ connect_url = $baseURL }

$buildHost = $Rebuild -or -not (Test-Path -LiteralPath $demoExe) -or -not (Test-Path -LiteralPath $cliExe)
if ($buildHost) { Build-DemoHost }
$buildExtension = $Rebuild -or -not (Test-Path -LiteralPath (Join-Path $releasePath 'octoeverywhere_windows_amd64.exe')) -or -not (Test-Path -LiteralPath (Join-Path $releasePath 'ui/index.js'))
if ($buildExtension) { Invoke-Tool pwsh @('-NoProfile', '-File', (Join-Path $PSScriptRoot 'build.ps1'), 'Release') }

$version = ((Get-Content -LiteralPath (Join-Path $releasePath 'module.yaml') | Where-Object { $_ -match '^version:' } | Select-Object -First 1) -replace '^version:\s*', '').Trim()
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'The release has no supported extension version.' }
$fingerprint = Get-ReleaseFingerprint
$needsInstall = $Rebuild -or -not (Test-Path -LiteralPath $installedFile) -or -not (Test-Path -LiteralPath $configFile)
if (Test-Path -LiteralPath $installedFile) {
    $installed = Get-Content -LiteralPath $installedFile -Raw | ConvertFrom-Json
    if ($installed.PSObject.Properties['repo'] -and $installed.repo -ne $repoRoot) {
        Repair-MovedState $installed.repo
        $needsInstall = $true
    }
    if (-not $installed.PSObject.Properties['package'] -or $installed.package -ne $fingerprint -or $installed.version -ne $version) {
        $needsInstall = $true
    }
}

if ($needsInstall) {
    if (-not (Test-Path -LiteralPath $configFile)) {
        Write-Host 'Creating the isolated demo server configuration.'
        $null = Start-OwnedProcess
        Stop-OwnedProcess
    }
    Write-Host 'Installing OctoEverywhere into the local simulator.'
    Invoke-Tool $cliExe @('module', 'add', "$modulePath@$version", '-config', $configFile, '-from', $releasePath, '-settings', $settingsFile, '-approve')
    Write-JSON $installedFile @{ repo = $repoRoot; version = $version; package = $fingerprint }
}

Write-Host 'Starting the loopback-only Connect simulator.'
$null = Start-OwnedProcess
try { Initialize-CameraAccess }
catch {
    Write-Warning 'The demo is running, but automatic camera setup did not finish. The extension page and logs show more information.'
    Write-Warning $_.Exception.Message
}
Show-Connection

[CmdletBinding()]
param(
    [Parameter(Position = 0, ValueFromRemainingArguments = $true)]
    [string[]]$ScriptArguments
)

$ErrorActionPreference = "Stop"
$Action = $null
$RequestedPort = $null

if ([string]::IsNullOrWhiteSpace($env:NEW_API_ROOT_DIR)) {
    $RootDir = Split-Path -Parent $MyInvocation.MyCommand.Path
} else {
    $RootDir = [System.IO.Path]::GetFullPath($env:NEW_API_ROOT_DIR)
}
$BuildDir = Join-Path $RootDir "build"
$RunDir = Join-Path $RootDir ".run"
$LogDir = Join-Path $RootDir "logs"
$BinaryPath = Join-Path $BuildDir "new-api.exe"
$PidFile = Join-Path $RunDir "new-api.pid"
$PortFile = Join-Path $RunDir "new-api.port"
$StdoutLog = Join-Path $RunDir "new-api.stdout.log"
$StderrLog = Join-Path $RunDir "new-api.stderr.log"

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command not found: $Name"
    }
}

function Resolve-PortValue {
    param(
        [string]$Value,
        [string]$Source
    )

    if ($Value -notmatch "^\d{1,5}$") {
        throw "$Source must be an integer between 1 and 65535."
    }

    $port = [int]$Value
    if ($port -lt 1 -or $port -gt 65535) {
        throw "$Source must be between 1 and 65535."
    }

    return $port
}

$validActions = @("build", "start", "stop", "restart", "rebuild", "running", "status", "logs", "help", "-h", "--help")
try {
    for ($index = 0; $index -lt $ScriptArguments.Count; $index++) {
        $argument = $ScriptArguments[$index]
        if ($argument -eq "--port") {
            if ($null -ne $RequestedPort) {
                throw "--port may only be specified once."
            }
            if ($index + 1 -ge $ScriptArguments.Count) {
                throw "--port requires a value."
            }
            $index++
            $RequestedPort = Resolve-PortValue $ScriptArguments[$index] "--port"
            continue
        }

        if ($argument.StartsWith("--port=", [System.StringComparison]::Ordinal)) {
            if ($null -ne $RequestedPort) {
                throw "--port may only be specified once."
            }
            $RequestedPort = Resolve-PortValue $argument.Substring("--port=".Length) "--port"
            continue
        }

        if ($validActions -contains $argument) {
            if ($null -ne $Action) {
                throw "Multiple commands were specified: $Action and $argument."
            }
            $Action = $argument
            continue
        }

        throw "Unknown argument: $argument"
    }

    if ($null -eq $Action) {
        $Action = "start"
    }
} catch {
    Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

function Invoke-Native {
    param(
        [scriptblock]$Command,
        [string]$FailureMessage
    )

    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$FailureMessage (exit code $LASTEXITCODE)"
    }
}

function Get-AppVersion {
    $versionFile = Join-Path $RootDir "VERSION"
    if (Test-Path -LiteralPath $versionFile) {
        $version = Get-Content -LiteralPath $versionFile -Raw
        if (-not [string]::IsNullOrWhiteSpace($version)) {
            return $version.Trim()
        }
    }

    if (Get-Command git -ErrorAction SilentlyContinue) {
        $version = (& git -C $RootDir describe --tags --always --dirty 2>$null)
        if ($LASTEXITCODE -eq 0 -and $version) {
            return $version.Trim()
        }
    }

    return "dev"
}

function Initialize-BuildEnvironment {
    $cacheDir = Join-Path $RootDir ".cache"
    $tempDir = Join-Path $cacheDir "tmp"
    $goCacheDir = Join-Path $cacheDir "go-build"
    $goModCacheDir = Join-Path $cacheDir "go-mod"
    $bunCacheDir = Join-Path $cacheDir "bun"

    New-Item -ItemType Directory -Force $tempDir, $goCacheDir, $goModCacheDir, $bunCacheDir | Out-Null

    $env:TEMP = $tempDir
    $env:TMP = $tempDir
    $env:GOCACHE = $goCacheDir
    $env:GOMODCACHE = $goModCacheDir
    $env:BUN_INSTALL_CACHE_DIR = $bunCacheDir
}

function Get-ManagedProcess {
    if (-not (Test-Path -LiteralPath $PidFile)) {
        return $null
    }

    $pidText = Get-Content -LiteralPath $PidFile -Raw
    if ([string]::IsNullOrWhiteSpace($pidText)) {
        Remove-Item -LiteralPath $PidFile, $PortFile -Force -ErrorAction SilentlyContinue
        return $null
    }
    $pidText = $pidText.Trim()
    $managedPid = 0
    if (-not [int]::TryParse($pidText, [ref]$managedPid)) {
        Remove-Item -LiteralPath $PidFile, $PortFile -Force -ErrorAction SilentlyContinue
        return $null
    }

    $process = Get-Process -Id $managedPid -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        Remove-Item -LiteralPath $PidFile, $PortFile -Force -ErrorAction SilentlyContinue
        return $null
    }

    $expectedPath = [System.IO.Path]::GetFullPath($BinaryPath)
    $actualPath = $null
    try {
        $actualPath = $process.Path
    } catch {
        $actualPath = $null
    }

    if ($actualPath -and -not [string]::Equals(
        [System.IO.Path]::GetFullPath($actualPath),
        $expectedPath,
        [System.StringComparison]::OrdinalIgnoreCase
    )) {
        throw "PID $managedPid belongs to another process: $actualPath"
    }

    return $process
}

function Get-ConfiguredPort {
    if ($null -ne $RequestedPort) {
        return $RequestedPort
    }

    if (-not [string]::IsNullOrWhiteSpace($env:PORT)) {
        return Resolve-PortValue $env:PORT "PORT"
    }

    $envFile = Join-Path $RootDir ".env"
    if (Test-Path -LiteralPath $envFile) {
        foreach ($line in Get-Content -LiteralPath $envFile) {
            if ($line -match "^\s*PORT\s*=\s*['`"]?(\d+)['`"]?\s*(?:#.*)?$") {
                return Resolve-PortValue $Matches[1] "PORT in .env"
            }
        }
    }

    return 3000
}

function Get-RunningPort {
    if (Test-Path -LiteralPath $PortFile) {
        $storedPort = (Get-Content -LiteralPath $PortFile -Raw).Trim()
        try {
            return Resolve-PortValue $storedPort "stored runtime port"
        } catch {
            Remove-Item -LiteralPath $PortFile -Force -ErrorAction SilentlyContinue
        }
    }

    return Get-ConfiguredPort
}

function Wait-ForStartup {
    param(
        [System.Diagnostics.Process]$Process,
        [int]$Port
    )

    $timeout = 60
    if ($env:STARTUP_TIMEOUT_SECONDS -match "^\d+$") {
        $timeout = [int]$env:STARTUP_TIMEOUT_SECONDS
    }

    $url = "http://127.0.0.1:$Port/api/status"
    $deadline = (Get-Date).AddSeconds($timeout)
    while ((Get-Date) -lt $deadline) {
        $Process.Refresh()
        if ($Process.HasExited) {
            throw "Process exited during startup. Check $StderrLog"
        }

        try {
            $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
            if ($response.StatusCode -eq 200) {
                Write-Host "Started: $url"
                return
            }
        } catch {
            Start-Sleep -Milliseconds 500
        }
    }

    throw "Startup timed out after $timeout seconds. Process is still running; check $StdoutLog and $StderrLog"
}

function Build-App {
    if (Get-ManagedProcess) {
        throw "The service is running. Use 'rebuild' to stop, build, and start it."
    }

    Require-Command "bun"
    Require-Command "go"
    Initialize-BuildEnvironment

    $version = Get-AppVersion
    Write-Step "Installing frontend dependencies"
    Push-Location (Join-Path $RootDir "web")
    try {
        Invoke-Native { bun install --frozen-lockfile } "Frontend dependency installation failed"
    } finally {
        Pop-Location
    }

    Write-Step "Building default frontend"
    Push-Location (Join-Path $RootDir "web/default")
    try {
        $env:DISABLE_ESLINT_PLUGIN = "true"
        $env:VITE_REACT_APP_VERSION = $version
        Invoke-Native { bun run build } "Default frontend build failed"
    } finally {
        Remove-Item Env:DISABLE_ESLINT_PLUGIN -ErrorAction SilentlyContinue
        Remove-Item Env:VITE_REACT_APP_VERSION -ErrorAction SilentlyContinue
        Pop-Location
    }

    Write-Step "Building classic frontend"
    Push-Location (Join-Path $RootDir "web/classic")
    try {
        $env:VITE_REACT_APP_VERSION = $version
        Invoke-Native { bun run build } "Classic frontend build failed"
    } finally {
        Remove-Item Env:VITE_REACT_APP_VERSION -ErrorAction SilentlyContinue
        Pop-Location
    }

    Write-Step "Building backend with embedded frontend assets"
    New-Item -ItemType Directory -Force $BuildDir | Out-Null
    Push-Location $RootDir
    try {
        $ldflags = "-s -w -X github.com/QuantumNous/new-api/common.Version=$version"
        Invoke-Native { go build -trimpath -ldflags $ldflags -o $BinaryPath . } "Backend build failed"
    } finally {
        Pop-Location
    }

    Write-Host "Build complete: $BinaryPath"
}

function Start-App {
    $running = Get-ManagedProcess
    if ($running) {
        $activePort = Get-RunningPort
        if ($null -ne $RequestedPort -and $RequestedPort -ne $activePort) {
            throw "Service is already running on port $activePort. Use 'restart --port $RequestedPort' to change it."
        }
        Write-Host "Already running (PID $($running.Id), http://127.0.0.1:$activePort)."
        return
    }

    if (-not (Test-Path -LiteralPath $BinaryPath)) {
        Build-App
    }

    New-Item -ItemType Directory -Force $RunDir, $LogDir | Out-Null
    New-Item -ItemType File -Force $StdoutLog, $StderrLog | Out-Null

    $port = Get-ConfiguredPort
    Write-Step "Starting service"
    $hadPortEnvironment = Test-Path Env:PORT
    $previousPortEnvironment = $env:PORT
    try {
        $env:PORT = [string]$port
        $process = Start-Process `
            -FilePath $BinaryPath `
            -ArgumentList @("--port", $port, "--log-dir", $LogDir) `
            -WorkingDirectory $RootDir `
            -RedirectStandardOutput $StdoutLog `
            -RedirectStandardError $StderrLog `
            -WindowStyle Hidden `
            -PassThru
    } finally {
        if ($hadPortEnvironment) {
            $env:PORT = $previousPortEnvironment
        } else {
            Remove-Item Env:PORT -ErrorAction SilentlyContinue
        }
    }

    Set-Content -LiteralPath $PidFile -Value $process.Id -NoNewline
    Set-Content -LiteralPath $PortFile -Value $port -NoNewline
    try {
        Wait-ForStartup $process $port
    } catch {
        Write-Host "Startup failed. Recent stderr:" -ForegroundColor Red
        if (Test-Path -LiteralPath $StderrLog) {
            Get-Content -LiteralPath $StderrLog -Tail 30
        }
        throw
    }
}

function Stop-App {
    $process = Get-ManagedProcess
    if (-not $process) {
        Write-Host "Service is not running."
        return
    }

    Write-Step "Stopping service (PID $($process.Id))"
    Stop-Process -Id $process.Id
    Wait-Process -Id $process.Id -Timeout 30 -ErrorAction SilentlyContinue
    if (Get-Process -Id $process.Id -ErrorAction SilentlyContinue) {
        Stop-Process -Id $process.Id -Force
        Wait-Process -Id $process.Id -Timeout 10 -ErrorAction SilentlyContinue
    }
    if (Get-Process -Id $process.Id -ErrorAction SilentlyContinue) {
        throw "Process $($process.Id) did not stop; runtime metadata was retained."
    }
    Remove-Item -LiteralPath $PidFile, $PortFile -Force -ErrorAction SilentlyContinue
    Write-Host "Stopped."
}

function Show-Status {
    $process = Get-ManagedProcess
    if (-not $process) {
        Write-Host "Status: stopped"
        return $false
    }

    $url = "http://127.0.0.1:$(Get-RunningPort)/api/status"
    try {
        $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 3
        Write-Host "Status: running (PID $($process.Id), HTTP $($response.StatusCode), $url)"
        return $true
    } catch {
        Write-Host "Status: process running (PID $($process.Id)), but health check failed: $url"
        return $false
    }
}

function Show-Logs {
    New-Item -ItemType Directory -Force $RunDir | Out-Null
    New-Item -ItemType File -Force $StdoutLog, $StderrLog | Out-Null
    Write-Host "Following $StdoutLog and $StderrLog. Press Ctrl+C to stop."
    Get-Content -LiteralPath $StdoutLog, $StderrLog -Tail 100 -Wait
}

function Show-Help {
    @"
Usage:
  .\new-api.cmd [command] [--port <port>]

Examples:
  .\new-api.cmd build
  .\new-api.cmd start --port 3000
  .\new-api.cmd stop
  .\new-api.cmd restart --port 8080
  .\new-api.cmd rebuild --port 8080
  .\new-api.cmd status
  .\new-api.cmd logs

Commands:
  build     Build both frontends and the Go binary.
  start     Start the existing binary; build first when it is missing.
  stop      Stop the managed background process.
  restart   Restart without rebuilding.
  rebuild   Stop, rebuild everything, and start.
  running   Return success when the managed process exists.
  status    Show process and HTTP health status.
  logs      Follow stdout and stderr logs.

Options:
  --port    Set the startup and health-check port (1-65535).
            This overrides PORT and the PORT value in .env.

Runtime configuration is loaded from .env by the application.
"@ | Write-Host
}

Set-Location $RootDir
try {
    switch ($Action) {
        "build" {
            Build-App
        }
        "start" {
            Start-App
        }
        "stop" {
            Stop-App
        }
        "restart" {
            if ($null -eq $RequestedPort) {
                $running = Get-ManagedProcess
                if ($running) {
                    $RequestedPort = Get-RunningPort
                }
            }
            Stop-App
            Start-App
        }
        "rebuild" {
            if ($null -eq $RequestedPort) {
                $running = Get-ManagedProcess
                if ($running) {
                    $RequestedPort = Get-RunningPort
                }
            }
            Stop-App
            Build-App
            Start-App
        }
        "running" {
            $running = Get-ManagedProcess
            if (-not $running) {
                Write-Host "Status: stopped"
                exit 1
            }
            Write-Host "Status: running (PID $($running.Id))"
        }
        "status" {
            if (-not (Show-Status)) {
                exit 1
            }
        }
        "logs" {
            Show-Logs
        }
        { $_ -in @("help", "-h", "--help") } {
            Show-Help
        }
    }
} catch {
    Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

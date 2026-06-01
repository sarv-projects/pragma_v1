# Pragma AI Installer for Windows (PowerShell)
# Usage: irm https://raw.githubusercontent.com/sarv-projects/pragma/main/install.ps1 | iex
# Or locally: .\install.ps1

$ErrorActionPreference = "Stop"

Write-Host "Installing Pragma AI..." -ForegroundColor Cyan

# Check prerequisites
function Test-Command($cmd) {
    return [bool](Get-Command $cmd -ErrorAction SilentlyContinue)
}

if (-not (Test-Command "go")) {
    Write-Host "Error: 'go' is not installed or not in PATH." -ForegroundColor Red
    Write-Host "Please install Go 1.24+ from https://golang.org/dl/"
    exit 1
}

if (-not (Test-Command "npm")) {
    Write-Host "Error: 'npm' is not installed or not in PATH." -ForegroundColor Red
    Write-Host "Please install Node.js 18+ from https://nodejs.org"
    exit 1
}

# Install uv if not present
if (-not (Test-Command "uv")) {
    Write-Host "Installing uv (Python package manager)..." -ForegroundColor Yellow
    irm https://astral.sh/uv/install.ps1 | iex
    # Refresh PATH
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "User") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "Machine")
    if (-not (Test-Command "uv")) {
        Write-Host "Error: uv installation failed or not found in PATH." -ForegroundColor Red
        exit 1
    }
}

Write-Host "Using uv: $(uv --version)" -ForegroundColor Green

# 1. Setup Python Daemon with uv
Write-Host "Setting up Python daemon environment..." -ForegroundColor Cyan
Push-Location daemon
if (-not (Test-Path ".venv")) {
    uv venv
}
uv pip install -e .
$DaemonDir = (Get-Location).Path
Pop-Location

$VenvPy = Join-Path $DaemonDir ".venv\Scripts\python.exe"

# 2. Build Web UI (Svelte SPA)
Write-Host "Building web UI..." -ForegroundColor Cyan
Push-Location web
npm install --silent 2>$null
npm run build
Pop-Location

# 3. Build Go Binary
Write-Host "Building Go CLI..." -ForegroundColor Cyan
$InstallDir = Join-Path $env:USERPROFILE ".local\bin"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$BinaryPath = Join-Path $InstallDir "pragma.exe"
go build -o $BinaryPath ./cmd/pragma
if ($LASTEXITCODE -ne 0) {
    Write-Host "Error: Failed to compile Go binary." -ForegroundColor Red
    exit 1
}

# 4. Wire the daemon interpreter into the config
$ConfigDir = Join-Path $env:USERPROFILE ".pragma"
$ConfigFile = Join-Path $ConfigDir "config.toml"
if (-not (Test-Path $ConfigDir)) {
    New-Item -ItemType Directory -Path $ConfigDir -Force | Out-Null
}

if (-not (Test-Path $ConfigFile)) {
    $ConfigContent = @"
# Pragma configuration. Env vars (PRAGMA_MODE, etc.) override these.
mode = "fast"
profile = "fastapi-async"

[budget]
lifetime_cap = 2.0
per_run_cap = 0.25

[output]
directory = "./output"
git_init = true

[daemon]
python_executable = "$($VenvPy -replace '\\', '\\')"
"@
    Set-Content -Path $ConfigFile -Value $ConfigContent
    Write-Host "Wrote default config to $ConfigFile" -ForegroundColor Green
} else {
    $content = Get-Content $ConfigFile -Raw
    $escapedPy = $VenvPy -replace '\\', '\\\\'
    if ($content -match '(?m)^\s*python_executable') {
        $content = $content -replace '(?m)^\s*python_executable\s*=.*', "python_executable = `"$escapedPy`""
    } elseif ($content -match '\[daemon\]') {
        $content = $content -replace '(\[daemon\])', "`$1`npython_executable = `"$escapedPy`""
    } else {
        $content += "`n`n[daemon]`npython_executable = `"$escapedPy`"`n"
    }
    Set-Content -Path $ConfigFile -Value $content
    Write-Host "Updated daemon.python_executable in $ConfigFile" -ForegroundColor Green
}

# 5. Add to PATH if not already there
$UserPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    $NewPath = "$InstallDir;$UserPath"
    [System.Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    $env:Path = "$InstallDir;$env:Path"
    Write-Host "Added $InstallDir to user PATH" -ForegroundColor Green
}

Write-Host ""
Write-Host "Pragma installed successfully!" -ForegroundColor Green
Write-Host ""
Write-Host "To get started:"
Write-Host "  pragma" -ForegroundColor White
Write-Host ""
Write-Host "This opens your browser. Paste your DeepSeek API key on first run."
Write-Host "Get one at: https://platform.deepseek.com (~`$0.03/project, `$2 minimum)"
Write-Host ""
Write-Host "Need to install the Python daemon separately?" -ForegroundColor Cyan
Write-Host "  pragma setup" -ForegroundColor White
Write-Host ""
Write-Host "Or use any OpenAI-compatible provider (BYOK) - configure in Settings."

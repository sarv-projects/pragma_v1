# bootstrap-daemon.ps1 — Install the Pragma Python daemon on Windows.
#
# Usage (after downloading pragma.exe):
#   iwr -useb https://raw.githubusercontent.com/sarv-projects/pragma/main/scripts/bootstrap-daemon.ps1 | iex
#
# What this does:
#   1. Checks Python 3.11+ is installed
#   2. Creates %USERPROFILE%\.pragma\venv
#   3. Installs the daemon from GitHub
#   4. Updates %USERPROFILE%\.pragma\config.toml with the venv Python path

$ErrorActionPreference = "Stop"

$PRAGMA_DIR = Join-Path $env:USERPROFILE ".pragma"
$VENV_DIR   = Join-Path $PRAGMA_DIR "venv"
$REPO       = "sarv-projects/pragma"
$BRANCH     = "main"
$DAEMON_PKG = "https://github.com/$REPO/archive/refs/heads/$BRANCH.tar.gz#subdirectory=daemon"

Write-Host ""
Write-Host "  Pragma Daemon Bootstrap (Windows)" -ForegroundColor Cyan
Write-Host "  ===================================" -ForegroundColor Cyan
Write-Host ""

# ── Step 1: Find Python 3.11+ ───────────────────────────────────────────────
Write-Host "[info]  Looking for Python 3.11+..." -ForegroundColor Blue

$PYTHON_CMD = $null
foreach ($candidate in @("python3.13", "python3.12", "python3.11", "python3", "python")) {
    try {
        $verOutput = & $candidate -c "import sys; print(sys.version_info.major, sys.version_info.minor)" 2>$null
        if ($verOutput) {
            $parts = $verOutput.Trim().Split(" ")
            $major = [int]$parts[0]; $minor = [int]$parts[1]
            if ($major -gt 3 -or ($major -eq 3 -and $minor -ge 11)) {
                $PYTHON_CMD = $candidate; break
            }
        }
    } catch { }
}

if (-not $PYTHON_CMD) {
    Write-Host "[fail]  Python 3.11+ not found." -ForegroundColor Red
    Write-Host "        Install from https://www.python.org/downloads/ then re-run." -ForegroundColor Red
    exit 1
}

$pyVer = (& $PYTHON_CMD --version 2>&1).ToString().Trim()
Write-Host "[ok]    Found $pyVer" -ForegroundColor Green

# ── Step 2: Create venv ─────────────────────────────────────────────────────
New-Item -ItemType Directory -Force -Path $PRAGMA_DIR | Out-Null

if (Test-Path $VENV_DIR) {
    Write-Host "[info]  Virtual environment already exists — skipping." -ForegroundColor Blue
} else {
    Write-Host "[info]  Creating virtual environment at $VENV_DIR..." -ForegroundColor Blue
    $uv = Get-Command uv -ErrorAction SilentlyContinue
    if ($uv) { & uv venv $VENV_DIR --python $PYTHON_CMD --quiet }
    else      { & $PYTHON_CMD -m venv $VENV_DIR }
    Write-Host "[ok]    Virtual environment created." -ForegroundColor Green
}

$VENV_PYTHON = Join-Path $VENV_DIR "Scripts\python.exe"
$VENV_PIP    = Join-Path $VENV_DIR "Scripts\pip.exe"

# ── Step 3: Install daemon ──────────────────────────────────────────────────
Write-Host "[info]  Installing Pragma daemon from GitHub ($REPO@$BRANCH)..." -ForegroundColor Blue
$uv = Get-Command uv -ErrorAction SilentlyContinue
if ($uv) { & uv pip install --python $VENV_PYTHON $DAEMON_PKG --quiet }
else      { & $VENV_PIP install $DAEMON_PKG --quiet }
Write-Host "[ok]    Daemon installed." -ForegroundColor Green

# ── Step 4: Update config ───────────────────────────────────────────────────
$CONFIG_FILE = Join-Path $PRAGMA_DIR "config.toml"
Write-Host "[info]  Updating config at $CONFIG_FILE..." -ForegroundColor Blue

if (-not (Test-Path $CONFIG_FILE)) {
    Set-Content -Path $CONFIG_FILE -Value "[daemon]`npython_executable = `"$VENV_PYTHON`"" -Encoding UTF8
    Write-Host "[ok]    Config created." -ForegroundColor Green
} else {
    $content = Get-Content $CONFIG_FILE -Raw
    if ($content -match "python_executable") {
        $content = $content -replace 'python_executable\s*=\s*"[^"]*"', "python_executable = `"$VENV_PYTHON`""
        Set-Content -Path $CONFIG_FILE -Value $content -Encoding UTF8
    } else {
        Add-Content -Path $CONFIG_FILE -Value "`n[daemon]`npython_executable = `"$VENV_PYTHON`"" -Encoding UTF8
    }
    Write-Host "[ok]    Config updated." -ForegroundColor Green
}

# ── Step 5: Verify ──────────────────────────────────────────────────────────
Write-Host "[info]  Verifying installation..." -ForegroundColor Blue
try {
    $result = & $VENV_PYTHON -c "import pragma_daemon; print('OK')" 2>&1
    if ($result -match "OK") { Write-Host "[ok]    Verification passed." -ForegroundColor Green }
    else { Write-Host "[warn]  Verification failed. Try: $VENV_PYTHON -m pip install -e .\daemon" -ForegroundColor Yellow }
} catch {
    Write-Host "[warn]  Could not verify installation." -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Bootstrap complete!" -ForegroundColor Green
Write-Host ""
Write-Host "  Python:  $VENV_PYTHON"
Write-Host "  Config:  $CONFIG_FILE"
Write-Host ""
Write-Host "Now run:  pragma.exe"
Write-Host "Open your browser and add your DeepSeek API key to get started."
Write-Host ""

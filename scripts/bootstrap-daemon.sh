#!/usr/bin/env bash
# bootstrap-daemon.sh — Install the Pragma Python daemon without cloning the repo.
#
# Usage (after downloading the pragma binary):
#   curl -sSL https://raw.githubusercontent.com/sarv-projects/pragma/main/scripts/bootstrap-daemon.sh | bash
#
# Or if you have the binary locally:
#   ./scripts/bootstrap-daemon.sh
#
# What this does:
#   1. Checks Python 3.11+ is installed
#   2. Creates ~/.pragma/venv
#   3. Installs the daemon from GitHub
#   4. Updates ~/.pragma/config.toml with the venv Python path
set -e

PRAGMA_DIR="$HOME/.pragma"
VENV_DIR="$PRAGMA_DIR/venv"
REPO="sarv-projects/pragma"
BRANCH="main"

# Colours
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'

info()    { echo -e "${BLUE}[info]${NC}  $1"; }
success() { echo -e "${GREEN}[ok]${NC}    $1"; }
warn()    { echo -e "${YELLOW}[warn]${NC}  $1"; }
fail()    { echo -e "${RED}[fail]${NC}  $1"; exit 1; }

echo ""
echo "  Pragma Daemon Bootstrap"
echo "  ========================"
echo ""

# ── Step 1: Find Python 3.11+ ──────────────────────────────────────────────
info "Looking for Python 3.11+..."
PYTHON_CMD=""
for candidate in python3.13 python3.12 python3.11 python3 python; do
    if command -v "$candidate" &>/dev/null; then
        version=$("$candidate" -c "import sys; print(sys.version_info.major, sys.version_info.minor)" 2>/dev/null || echo "")
        major=$(echo "$version" | awk '{print $1}')
        minor=$(echo "$version" | awk '{print $2}')
        if [ -n "$major" ] && { [ "$major" -gt 3 ] || { [ "$major" -eq 3 ] && [ "$minor" -ge 11 ]; }; }; then
            PYTHON_CMD="$candidate"
            break
        fi
    fi
done

if [ -z "$PYTHON_CMD" ]; then
    fail "Python 3.11+ not found. Install it from https://www.python.org/downloads/ and re-run."
fi
success "Found $("$PYTHON_CMD" --version 2>&1)"

# ── Step 2: Create ~/.pragma/venv ──────────────────────────────────────────
mkdir -p "$PRAGMA_DIR"
if [ -d "$VENV_DIR" ]; then
    info "Virtual environment already exists at $VENV_DIR — skipping."
else
    info "Creating virtual environment at $VENV_DIR..."
    if command -v uv &>/dev/null; then
        uv venv "$VENV_DIR" --python "$PYTHON_CMD" --quiet
    else
        "$PYTHON_CMD" -m venv "$VENV_DIR"
    fi
    success "Virtual environment created."
fi

VENV_PYTHON="$VENV_DIR/bin/python"

# ── Step 3: Install daemon ─────────────────────────────────────────────────
DAEMON_PKG="https://github.com/$REPO/archive/refs/heads/$BRANCH.tar.gz#subdirectory=daemon"
info "Installing Pragma daemon from GitHub ($REPO@$BRANCH)..."

if command -v uv &>/dev/null; then
    uv pip install --python "$VENV_PYTHON" "$DAEMON_PKG" --quiet
else
    "$VENV_DIR/bin/pip" install "$DAEMON_PKG" --quiet
fi
success "Daemon installed."

# ── Step 4: Update config ──────────────────────────────────────────────────
CONFIG_FILE="$PRAGMA_DIR/config.toml"
info "Updating config at $CONFIG_FILE..."

if [ ! -f "$CONFIG_FILE" ]; then
    printf '[daemon]\npython_executable = "%s"\n' "$VENV_PYTHON" > "$CONFIG_FILE"
    success "Config created."
else
    if grep -q "python_executable" "$CONFIG_FILE"; then
        # Use a temp file for sed portability (BSD vs GNU)
        TMP_CFG=$(mktemp)
        sed "s|^[[:space:]]*python_executable[[:space:]]*=.*|python_executable = \"$VENV_PYTHON\"|" "$CONFIG_FILE" > "$TMP_CFG"
        mv "$TMP_CFG" "$CONFIG_FILE"
    else
        printf '\n[daemon]\npython_executable = "%s"\n' "$VENV_PYTHON" >> "$CONFIG_FILE"
    fi
    success "Config updated."
fi

# ── Step 5: Verify ─────────────────────────────────────────────────────────
info "Verifying installation..."
if "$VENV_PYTHON" -c "import pragma_daemon; print('OK')" 2>/dev/null | grep -q "OK"; then
    success "Verification passed."
else
    warn "Verification failed — try: $VENV_PYTHON -m pip install -e ./daemon"
fi

echo ""
echo -e "${GREEN}Bootstrap complete!${NC}"
echo ""
echo "  Python:  $VENV_PYTHON"
echo "  Config:  $CONFIG_FILE"
echo ""
echo "Now run:  pragma"
echo "Open your browser and add your DeepSeek API key to get started."
echo ""

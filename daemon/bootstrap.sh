#!/usr/bin/env bash
# bootstrap.sh — Quick daemon setup for developers working from source.
#
# Usage:
#   cd daemon && ./bootstrap.sh
#
# This creates a .venv, installs the daemon in editable mode, and prints
# instructions for updating the Go binary config. For users who don't have
# the source tree, use scripts/bootstrap-daemon.sh instead.
#
# Requires: Python 3.11+, uv (recommended) or pip

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo ""
echo "  Pragma Daemon Bootstrap (development)"
echo "  ======================================"
echo ""

# ── Find Python 3.11+ ────────────────────────────────────────────────────────
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
    echo "Error: Python 3.11+ not found. Install from https://www.python.org/downloads/"
    exit 1
fi

echo "  1. Python:  $("$PYTHON_CMD" --version 2>&1)"

# ── Create / use .venv ───────────────────────────────────────────────────────
if [ -d ".venv" ]; then
    echo "  2. .venv:   Already exists — skipping"
else
    if command -v uv &>/dev/null; then
        uv venv --python "$PYTHON_CMD" --quiet
    else
        "$PYTHON_CMD" -m venv .venv
    fi
    echo "  2. .venv:   Created at .venv/"
fi

VENV_PY=".venv/bin/python"
[ -f "$VENV_PY" ] || VENV_PY=".venv/Scripts/python"

# ── Install daemon in editable mode ─────────────────────────────────────────
echo "  3. Install: Installing pragma_daemon in editable mode..."
if command -v uv &>/dev/null; then
    uv pip install --quiet -e .
else
    "$VENV_PY" -m pip install --quiet -e .
fi

# ── Verify ──────────────────────────────────────────────────────────────────
echo "  4. Verify:  Checking installation..."
if "$VENV_PY" -c "import pragma_daemon; print('OK')" 2>/dev/null | grep -q "OK"; then
    echo "              ✓ pragma_daemon importable"
else
    echo "              ⚠ Verification failed — check: $VENV_PY -m pip install -e ."
fi

# ── Done ────────────────────────────────────────────────────────────────────
echo ""
echo "  ✓ Development daemon ready!"
echo ""
echo "  To use this venv with the pragma binary, make sure your"
echo "  ~/.pragma/config.toml has:"
echo ""
echo "    [daemon]"
echo "    python_executable = \"$(realpath "$VENV_PY" 2>/dev/null || echo "$SCRIPT_DIR/$VENV_PY")\""
echo ""
echo "  Or run: pragma setup"
echo ""

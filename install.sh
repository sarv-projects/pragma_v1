#!/usr/bin/env bash
# install.sh — Source install (requires Go, Node, Python).
#
# For a release binary install (no source required), download the binary from
# https://github.com/sarv-projects/pragma/releases/latest and then run:
#   curl -sSL https://raw.githubusercontent.com/sarv-projects/pragma/main/scripts/bootstrap-daemon.sh | bash
# or on Windows PowerShell:
#   iwr -useb https://raw.githubusercontent.com/sarv-projects/pragma/main/scripts/bootstrap-daemon.ps1 | iex
#
set -e

echo "Installing Pragma AI (from source)..."

# Check prerequisites
if ! command -v go &> /dev/null; then
    echo "Error: 'go' is not installed or not in PATH."
    echo "Please install Go 1.24+ from https://golang.org/dl/"
    exit 1
fi

if ! command -v npm &> /dev/null; then
    echo "Error: 'npm' is not installed or not in PATH."
    echo "Please install Node.js 18+ from https://nodejs.org"
    exit 1
fi

# Install uv if not present
if ! command -v uv &> /dev/null; then
    echo "Installing uv (Python package manager)..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    # Source the env so uv is available in this session
    export PATH="$HOME/.local/bin:$PATH"
    if ! command -v uv &> /dev/null; then
        echo "Error: uv installation failed or not found in PATH."
        exit 1
    fi
fi

echo "Using uv: $(uv --version)"

# Detect sed -i syntax (GNU vs BSD/macOS)
if sed --version >/dev/null 2>&1; then
    # GNU sed (Linux)
    SED_INPLACE=(-i)
else
    # BSD sed (macOS)
    SED_INPLACE=(-i '')
fi

# 1. Setup Python Daemon with uv
echo "Setting up Python daemon environment..."
cd daemon
if [ ! -d ".venv" ]; then
    uv venv
fi
uv pip install -e .
DAEMON_DIR="$(pwd)"
cd ..

VENV_PY="$DAEMON_DIR/.venv/bin/python"

# 2. Build Web UI (Svelte SPA)
echo "Building web UI..."
cd web
npm install --silent 2>/dev/null
npm run build
cd ..

# 3. Build Go Binary (embeds web/build/ into the binary)
echo "Building Go CLI..."
mkdir -p "$HOME/.local/bin"

if ! go build -o "$HOME/.local/bin/pragma" ./cmd/pragma; then
    echo "Error: Failed to compile Go binary."
    exit 1
fi

# 4. Wire the daemon interpreter into the config.
CONFIG_DIR="$HOME/.pragma"
CONFIG_FILE="$CONFIG_DIR/config.toml"
mkdir -p "$CONFIG_DIR"

if [ ! -f "$CONFIG_FILE" ]; then
    cat > "$CONFIG_FILE" <<EOF
# Pragma configuration (see spec). Env vars (PRAGMA_MODE, etc.) override these.
mode = "fast"
profile = "fastapi-async"

[budget]
lifetime_cap = 2.0
per_run_cap = 0.25

[output]
directory = "./output"
git_init = true

[daemon]
python_executable = "$VENV_PY"
EOF
    echo "Wrote default config to $CONFIG_FILE"
else
    if grep -q '^[[:space:]]*python_executable' "$CONFIG_FILE"; then
        sed "${SED_INPLACE[@]}" "s|^[[:space:]]*python_executable.*|python_executable = \"$VENV_PY\"|" "$CONFIG_FILE"
    elif grep -q '^\[daemon\]' "$CONFIG_FILE"; then
        sed "${SED_INPLACE[@]}" "/^\[daemon\]/a python_executable = \"$VENV_PY\"" "$CONFIG_FILE"
    else
        printf '\n[daemon]\npython_executable = "%s"\n' "$VENV_PY" >> "$CONFIG_FILE"
    fi
    echo "Updated daemon.python_executable in $CONFIG_FILE"
fi

# 5. Ensure ~/.local/bin is in PATH
SHELL_RC=""
if [ -f "$HOME/.zshrc" ]; then
    SHELL_RC="$HOME/.zshrc"
elif [ -f "$HOME/.bashrc" ]; then
    SHELL_RC="$HOME/.bashrc"
fi

if [ -n "$SHELL_RC" ]; then
    if ! grep -q 'export PATH="$HOME/.local/bin:$PATH"' "$SHELL_RC" 2>/dev/null; then
        echo '' >> "$SHELL_RC"
        echo '# Added by Pragma installer' >> "$SHELL_RC"
        echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$SHELL_RC"
        echo "Added ~/.local/bin to PATH in $SHELL_RC"
        echo "Run: source $SHELL_RC (or open a new terminal)"
    fi
else
    if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
        echo "Note: Could not find ~/.bashrc or ~/.zshrc to update PATH."
        echo "Add this to your shell profile:"
        echo '  export PATH="$HOME/.local/bin:$PATH"'
    fi
fi

echo ""
echo "Pragma installed successfully!"
echo ""
echo "To get started:"
echo "  pragma"
echo ""
echo "This opens your browser. Paste your DeepSeek API key on first run."
echo "Get one at: https://platform.deepseek.com (~\$0.03/project, \$2 minimum)"
echo ""
echo "Need to install the Python daemon separately?"
echo "  pragma setup"
echo ""
echo "Or use any OpenAI-compatible provider (BYOK) - configure in Settings."

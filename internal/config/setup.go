package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DaemonSetupStatus describes the current state of daemon setup. Used by the
// health endpoint and SetupGuide to show granular setup progress.
type DaemonSetupStatus struct {
	PythonFound     bool   `json:"python_found"`
	PythonPath      string `json:"python_path,omitempty"`
	VenvExists      bool   `json:"venv_exists"`
	VenvPath        string `json:"venv_path,omitempty"`
	DaemonInstalled bool   `json:"daemon_installed"`
	ConfigWritten   bool   `json:"config_written"`
	AllOK           bool   `json:"all_ok"`
	Error           string `json:"error,omitempty"`
}

// CheckDaemonSetup inspects the local environment and reports the current state
// of daemon setup without modifying anything. This is safe to call frequently
// (no side effects).
func CheckDaemonSetup() *DaemonSetupStatus {
	status := &DaemonSetupStatus{}

	home, err := os.UserHomeDir()
	if err != nil {
		status.Error = "cannot determine home directory"
		return status
	}

	pragmaDir := filepath.Join(home, ".pragma")
	venvDir := filepath.Join(pragmaDir, "venv")
	venvPython := filepath.Join(venvDir, "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(venvDir, "Scripts", "python.exe")
	}

	// Check Python 3.11+ on PATH
	pythonPath, err := FindPython311()
	status.PythonFound = err == nil
	if status.PythonFound {
		status.PythonPath = pythonPath
	}

	// Check venv exists with a python binary
	if _, err := os.Stat(venvPython); err == nil {
		status.VenvExists = true
		status.VenvPath = venvPython
	}

	// Check daemon is importable from the venv
	if status.VenvExists {
		out, err := exec.Command(venvPython, "-c", "import pragma_daemon; print('OK')").CombinedOutput()
		status.DaemonInstalled = err == nil && strings.Contains(string(out), "OK")
	}

	// Check config has a non-default python_executable pointing to the venv
	cfgPath := DefaultPath()
	if _, err := os.Stat(cfgPath); err == nil {
		cfg, err := Load(cfgPath)
		if err == nil && cfg.Daemon.PythonExecutable != "" && cfg.Daemon.PythonExecutable != "python3" {
			status.ConfigWritten = true
		}
	}

	status.AllOK = status.PythonFound && status.VenvExists && status.DaemonInstalled && status.ConfigWritten
	return status
}

// EnsureSetupResult describes the outcome of EnsureDaemonSetup.
type EnsureSetupResult struct {
	Success bool   `json:"success"`
	Step    string `json:"step,omitempty"` // which step failed
	Message string `json:"message,omitempty"`
}

// EnsureDaemonSetup attempts to fully set up the daemon: find Python 3.11+,
// create a venv at ~/.pragma/venv, install the daemon package, and write
// the config. It stops at the first failure and returns a descriptive result.
func EnsureDaemonSetup() *EnsureSetupResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return &EnsureSetupResult{Step: "home", Message: "cannot determine home directory: " + err.Error()}
	}

	pragmaDir := filepath.Join(home, ".pragma")
	venvDir := filepath.Join(pragmaDir, "venv")
	venvPython := filepath.Join(venvDir, "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(venvDir, "Scripts", "python.exe")
	}

	// Step 1: Find Python 3.11+
	pythonPath, err := FindPython311()
	if err != nil {
		return &EnsureSetupResult{Step: "python", Message: "Python 3.11+ not found: " + err.Error()}
	}

	// Step 2: Create ~/.pragma/venv
	if err := os.MkdirAll(pragmaDir, 0755); err != nil {
		return &EnsureSetupResult{Step: "mkdir", Message: "cannot create " + pragmaDir + ": " + err.Error()}
	}
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		var createErr error
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			createErr = runCmd(uvPath, "venv", venvDir)
		} else {
			createErr = runCmd(pythonPath, "-m", "venv", venvDir)
		}
		if createErr != nil {
			return &EnsureSetupResult{Step: "venv", Message: "failed to create venv: " + createErr.Error()}
		}
	}

	// Step 3: Install the daemon package
	var installErr error
	// Probe for daemon source relative to the executable or cwd
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	daemonSrc := filepath.Join(execDir, "daemon")
	if _, err := os.Stat(filepath.Join(daemonSrc, "pyproject.toml")); err != nil {
		// Try relative to cwd (go run . scenario)
		cwd, _ := os.Getwd()
		daemonSrc = filepath.Join(cwd, "daemon")
	}

	if _, err := os.Stat(filepath.Join(daemonSrc, "pyproject.toml")); err == nil {
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			installErr = runCmd(uvPath, "pip", "install", "--python", venvPython, "-e", daemonSrc)
		} else {
			installErr = runCmd(venvPython, "-m", "pip", "install", "-e", daemonSrc)
		}
	} else {
		// No source — install from GitHub main branch
		daemonPkg := "https://github.com/sarv-projects/pragma/archive/refs/heads/main.tar.gz#subdirectory=daemon"
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			installErr = runCmd(uvPath, "pip", "install", "--python", venvPython, daemonPkg)
		} else {
			installErr = runCmd(venvPython, "-m", "pip", "install", daemonPkg)
		}
	}
	if installErr != nil {
		return &EnsureSetupResult{Step: "install", Message: "failed to install daemon: " + installErr.Error()}
	}

	// Step 4: Update config with the venv Python path
	cfg, _ := Load(DefaultPath())
	if cfg == nil {
		cfg = defaults()
	}
	cfg.Daemon.PythonExecutable = venvPython
	if err := cfg.Save(DefaultPath()); err != nil {
		return &EnsureSetupResult{Step: "config", Message: "failed to save config: " + err.Error()}
	}

	return &EnsureSetupResult{Success: true, Step: "done", Message: venvPython}
}

// FindPython311 locates Python 3.11+ on the system PATH.
func FindPython311() (string, error) {
	candidates := []string{"python3.12", "python3.11", "python3", "python"}
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if ok := checkPythonVersion(path, 3, 11); ok {
			return path, nil
		}
	}
	return "", fmt.Errorf("Python 3.11 or later not found in PATH")
}

// checkPythonVersion returns true if the given python binary is at least major.minor.
func checkPythonVersion(pythonPath string, major, minor int) bool {
	out, err := exec.Command(pythonPath, "-c",
		"import sys; print(sys.version_info.major, sys.version_info.minor)").Output()
	if err != nil {
		return false
	}
	var maj, min int
	if n, _ := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &maj, &min); n == 2 {
		return maj > major || (maj == major && min >= minor)
	}
	return false
}

// runCmd runs a command with its arguments, returning combined output on error.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

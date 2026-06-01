package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sarv-projects/pragma/internal/config"
)

func runSetup() int {
	fmt.Println("Pragma Setup")
	fmt.Println("============")
	fmt.Println("This command creates a Python virtual environment and installs the Pragma daemon.")
	fmt.Println()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %v\n", err)
		return 1
	}

	pragmaDir := filepath.Join(home, ".pragma")
	venvDir := filepath.Join(pragmaDir, "venv")

	// Step 1: Find Python 3.11+
	fmt.Print("1. Checking for Python 3.11+... ")
	pythonPath, err := findPython311()
	if err != nil {
		fmt.Println("❌")
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please install Python 3.11 or later from https://www.python.org/downloads/\n")
		return 1
	}
	fmt.Printf("✅ Found: %s\n", pythonPath)

	// Step 2: Create venv at ~/.pragma/venv
	fmt.Printf("2. Creating virtual environment at %s... ", venvDir)
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		// Try uv first (faster), fall back to venv
		var createErr error
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			createErr = runCmd(uvPath, "venv", venvDir)
		} else {
			createErr = runCmd(pythonPath, "-m", "venv", venvDir)
		}
		if createErr != nil {
			fmt.Println("❌")
			fmt.Fprintf(os.Stderr, "\nError creating venv: %v\n", createErr)
			return 1
		}
		fmt.Println("✅")
	} else {
		fmt.Println("✅ Already exists")
	}

	// Step 3: Get the venv Python path
	venvPython := filepath.Join(venvDir, "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(venvDir, "Scripts", "python.exe")
	}

	// Step 4: Install the daemon
	fmt.Print("3. Installing Pragma daemon... ")

	// Check if daemon source is available next to binary (developer install)
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	daemonSrc := filepath.Join(execDir, "daemon")
	if _, err := os.Stat(filepath.Join(daemonSrc, "pyproject.toml")); err != nil {
		// Try relative to cwd (go run . scenario)
		cwd, _ := os.Getwd()
		daemonSrc = filepath.Join(cwd, "daemon")
	}

	var installErr error
	if _, err := os.Stat(filepath.Join(daemonSrc, "pyproject.toml")); err == nil {
		// Source available — editable install
		fmt.Printf("(from source at %s)... ", daemonSrc)
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			installErr = runCmd(uvPath, "pip", "install", "--python", venvPython, "-e", daemonSrc)
		} else {
			pipPath := filepath.Join(venvDir, "bin", "pip")
			if runtime.GOOS == "windows" {
				pipPath = filepath.Join(venvDir, "Scripts", "pip.exe")
			}
			installErr = runCmd(pipPath, "install", "-e", daemonSrc)
		}
	} else {
		// No source — install from GitHub
		// Use main branch for latest development version
		// For release installations, use tagged versions (via pragma upgrade or bootstrap scripts)
		daemonPkg := "https://github.com/sarv-projects/pragma/archive/refs/heads/main.tar.gz#subdirectory=daemon"
		fmt.Printf("(from GitHub main branch)... ")
		if uvPath, uvErr := exec.LookPath("uv"); uvErr == nil {
			installErr = runCmd(uvPath, "pip", "install", "--python", venvPython, daemonPkg)
		} else {
			pipPath := filepath.Join(venvDir, "bin", "pip")
			if runtime.GOOS == "windows" {
				pipPath = filepath.Join(venvDir, "Scripts", "pip.exe")
			}
			installErr = runCmd(pipPath, "install", daemonPkg)
		}
	}

	if installErr != nil {
		fmt.Println("❌")
		fmt.Fprintf(os.Stderr, "\nError installing daemon: %v\n", installErr)
		fmt.Fprintf(os.Stderr, "Try manually: %s -m pip install -e ./daemon\n", venvPython)
		return 1
	}
	fmt.Println("✅")

	// Step 5: Update config with python_executable
	fmt.Print("4. Updating config... ")
	cfg, _ := config.Load(config.DefaultPath())
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.Daemon.PythonExecutable = venvPython
	if err := cfg.Save(config.DefaultPath()); err != nil {
		fmt.Println("⚠️  (could not save config, but setup succeeded)")
	} else {
		fmt.Println("✅")
	}

	// Step 6: Quick verification — check daemon can be imported
	fmt.Print("5. Verifying installation... ")
	out, err := exec.Command(venvPython, "-c", "import pragma_daemon; print('OK')").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "OK") {
		fmt.Println("⚠️  (verification failed, but daemon may still work)")
		fmt.Printf("   Output: %s\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Println("✅")
	}

	fmt.Println()
	fmt.Println("✅ Setup complete!")
	fmt.Printf("   Python: %s\n", venvPython)
	fmt.Printf("   Config: %s\n", config.DefaultPath())
	fmt.Println()
	fmt.Println("Now run: pragma")
	fmt.Println("Then open your browser and add your API keys.")
	return 0
}

// findPython311 locates Python 3.11+ on the system.
func findPython311() (string, error) {
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

// runCmd runs a command with its arguments, printing combined output on error.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

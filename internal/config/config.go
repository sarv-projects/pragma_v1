package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Mode constants. Pragma is DeepSeek-only, so "fast" is the only mode. The
// field is retained so config files and the settings API stay stable.
const (
	ModeFast = "fast" // DeepSeek direct API, paid
)

type Config struct {
	Mode     string         `toml:"mode" json:"mode"`
	Budget   BudgetConfig   `toml:"budget" json:"budget"`
	Output   OutputConfig   `toml:"output" json:"output"`
	Profile  string         `toml:"profile" json:"profile"`
	Daemon   DaemonConfig   `toml:"daemon" json:"daemon"`
	Provider ProviderConfig `toml:"provider" json:"provider"`
}

type BudgetConfig struct {
	LifetimeCap float64 `toml:"lifetime_cap" json:"lifetime_cap"`
	PerRunCap   float64 `toml:"per_run_cap" json:"per_run_cap"`
}

type OutputConfig struct {
	Directory string `toml:"directory" json:"directory"`
	GitInit   bool   `toml:"git_init" json:"git_init"`
}

type DaemonConfig struct {
	PythonExecutable string `toml:"python_executable" json:"python_executable"`
}

// ProviderConfig holds BYOK (Bring Your Own Key) settings for the codegen LLM.
// DeepSeek is the default. Users can switch to any OpenAI-compatible provider.
type ProviderConfig struct {
	Name            string `toml:"name" json:"name"`                         // "deepseek", "openai", "ollama", "openrouter", "custom"
	BaseURL         string `toml:"base_url" json:"base_url"`                 // API endpoint
	ReasoningModel  string `toml:"reasoning_model" json:"reasoning_model"`   // Model for spec compilation (thinking)
	CodegenModel    string `toml:"codegen_model" json:"codegen_model"`       // Model for code generation
	SupportsThinking bool  `toml:"supports_thinking" json:"supports_thinking"` // Whether model supports thinking/reasoning mode
}


func defaults() *Config {
	return &Config{
		Mode: ModeFast,
		Budget: BudgetConfig{
			LifetimeCap: 2.00,
			PerRunCap:   0.25,
		},
		Output: OutputConfig{
			Directory: "./output",
			GitInit:   true,
		},
		Profile: "fastapi-async",
		Daemon: DaemonConfig{
			PythonExecutable: "python3",
		},
		Provider: ProviderConfig{
			Name:             "deepseek",
			BaseURL:          "https://api.deepseek.com",
			ReasoningModel:   "",
			CodegenModel:     "",
			SupportsThinking: true,
		},
	}
}

func Load(path string) (*Config, error) {
	c := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnvOverrides(c)
			resolvePythonPath(c)
			resolveOutputDir(c)
			return c, nil
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, c); err != nil {
		return nil, err
	}

	if c.Mode == "" {
		c.Mode = ModeFast
	}

	applyEnvOverrides(c)
	resolvePythonPath(c)
	resolveOutputDir(c)
	return c, nil
}

// resolvePythonPath probes for the venv Python first, then falls back to python3.
// This allows release users (who ran pragma setup or bootstrap) to work without
// manually setting python_executable in config.
func resolvePythonPath(c *Config) {
	// If already set to a specific non-default path, respect it
	if c.Daemon.PythonExecutable != "" && c.Daemon.PythonExecutable != "python3" {
		return
	}
	// Probe ~/.pragma/venv/bin/python (Linux/Mac) or Scripts/python.exe (Windows)
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	venvPython := filepath.Join(home, ".pragma", "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(home, ".pragma", "venv", "Scripts", "python.exe")
	}
	if _, err := os.Stat(venvPython); err == nil {
		c.Daemon.PythonExecutable = venvPython
	}
}

// resolveOutputDir converts a relative output directory to absolute using cwd.
func resolveOutputDir(c *Config) {
	if !filepath.IsAbs(c.Output.Directory) && !strings.HasPrefix(c.Output.Directory, "~") {
		if abs, err := filepath.Abs(c.Output.Directory); err == nil {
			c.Output.Directory = abs
		}
	}
}

// applyEnvOverrides applies PRAGMA_* environment variables over config values
// (spec §18.2). Env wins over config.toml.
func applyEnvOverrides(c *Config) {
	// "fast" (DeepSeek) is the only valid mode. Any non-empty PRAGMA_MODE
	// value is normalised to "fast".
	if strings.TrimSpace(os.Getenv("PRAGMA_MODE")) != "" {
		c.Mode = ModeFast
	}
	if v := os.Getenv("PRAGMA_OUTPUT"); v != "" {
		c.Output.Directory = v
	}
	if v := os.Getenv("PRAGMA_PROFILE"); v != "" {
		c.Profile = v
	}
}

func DefaultPath() string {
	return filepath.Join(configDir(), "config.toml")
}

// BudgetPath returns ~/.pragma/budget.json.
func BudgetPath() string {
	return filepath.Join(configDir(), "budget.json")
}

// LedgerPath returns ~/.pragma/ledger.json.
func LedgerPath() string {
	return filepath.Join(configDir(), "ledger.json")
}

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".pragma")
}

func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

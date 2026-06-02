package keyvault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/zalando/go-keyring"
)

const DefaultService = "pragma"

// Key names as stored in the keyring / fallback file.
const (
	KeyDeepSeek = "deepseek"
	KeyGroq     = "groq"
	KeyCustom   = "custom"
)

// credFileMu serializes access to the fallback credentials file.
var credFileMu sync.Mutex

type Keyring struct {
	service string
}

func NewKeyring(service string) *Keyring {
	return &Keyring{service: service}
}

// isLinux reports whether the current OS is Linux (including WSL).
func isLinux() bool {
	return runtime.GOOS == "linux"
}

// credFilePath returns ~/.pragma/credentials.json — the fallback store used
// when the OS keyring is unavailable (common on WSL / headless Linux, where
// there's no D-Bus Secret Service). The file is created with 0600 perms.
func credFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pragma", "credentials.json")
}

func fileGet(name string) (string, bool) {
	credFileMu.Lock()
	defer credFileMu.Unlock()
	p := credFilePath()
	if p == "" {
		return "", false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return "", false
	}
	v, ok := m[name]
	return v, ok && v != ""
}

func fileSet(name, value string) error {
	credFileMu.Lock()
	defer credFileMu.Unlock()
	p := credFilePath()
	if p == "" {
		return errors.New("cannot resolve home directory for credential store")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	m := map[string]string{}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	m[name] = value
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// 0600: owner read/write only. Atomic: write to temp then rename.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func fileDelete(name string) {
	credFileMu.Lock()
	defer credFileMu.Unlock()
	p := credFilePath()
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	m := map[string]string{}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	delete(m, name)
	out, _ := json.MarshalIndent(m, "", "  ")
	tmp := p + ".tmp"
	_ = os.WriteFile(tmp, out, 0600)
	_ = os.Rename(tmp, p)
}

// Available reports whether keys can be persisted at all — either via the OS
// keyring or the file fallback. It never mutates the keyring (a probe Get of a
// sentinel; "not found" still means the keyring works).
func (k *Keyring) Available() bool {
	_, err := keyring.Get(k.service, "__pragma_probe__")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return true
	}
	// Keyring unusable — the file fallback is still available as long as we
	// can resolve a home directory.
	return credFilePath() != ""
}

// KeyringAvailable reports specifically whether the OS keyring backend works.
func (k *Keyring) KeyringAvailable() bool {
	_, err := keyring.Get(k.service, "__pragma_probe__")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Get reads a key, preferring the OS keyring and falling back to the file.
func (k *Keyring) Get(name string) (string, error) {
	if v, err := keyring.Get(k.service, name); err == nil {
		return v, nil
	}
	if v, ok := fileGet(name); ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}

// Set writes a key to the OS keyring, falling back to the 0600 file when the
// keyring backend is unavailable (so TUI-entered keys persist on WSL).
//
// On Linux we always ALSO write to the file fallback because the OS keyring
// (libsecret / D-Bus Secret Service) is frequently absent in WSL, Docker, and
// CI environments and may silently succeed (return nil) while not actually
// persisting anything.  The file write is cheap and ensures the key is always
// readable via EnvForDaemon even when the keyring backend is a no-op.
func (k *Keyring) Set(name, value string) error {
	keyringErr := keyring.Set(k.service, name, value)

	// On Linux always persist to the file too (handles WSL/headless silently
	// swallowing keyring writes without returning an error).
	if isLinux() || keyringErr != nil {
		if err := fileSet(name, value); err != nil {
			if keyringErr != nil {
				// Both failed — return the file error as it's more actionable.
				return err
			}
		}
	}
	return nil
}

func (k *Keyring) Delete(name string) error {
	_ = keyring.Delete(k.service, name)
	fileDelete(name)
	return nil
}

func SaveKeys(keys map[string]string) error {
	kr := NewKeyring(DefaultService)
	for k, v := range keys {
		if v == "" {
			continue // don't persist empty keys
		}
		if err := kr.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}

// EnvForDaemon reads stored API keys (keyring or file fallback) and maps them
// to the environment variable names the Python daemon expects. Keys already in
// the process environment are NOT overridden (env wins, per spec §18.2).
func EnvForDaemon(lookupEnv func(string) (string, bool)) []string {
	kr := NewKeyring(DefaultService)
	mapping := map[string]string{
		KeyDeepSeek: "DEEPSEEK_API_KEY",
		KeyGroq:     "GROQ_API_KEY",
		KeyCustom:   "CUSTOM_API_KEY",
	}
	var out []string
	for keyName, envName := range mapping {
		if _, present := lookupEnv(envName); present {
			continue // env var already set, leave it
		}
		if v, err := kr.Get(keyName); err == nil && v != "" {
			out = append(out, envName+"="+v)
		}
	}
	return out
}

// HasAnyKey reports whether at least one API key is available via env, the OS
// keyring, or the file fallback.
func HasAnyKey(lookupEnv func(string) (string, bool)) bool {
	for _, envName := range []string{"DEEPSEEK_API_KEY", "GROQ_API_KEY", "CUSTOM_API_KEY"} {
		if v, ok := lookupEnv(envName); ok && v != "" {
			return true
		}
	}
	kr := NewKeyring(DefaultService)
	for _, keyName := range []string{KeyDeepSeek, KeyGroq, KeyCustom} {
		if v, err := kr.Get(keyName); err == nil && v != "" {
			return true
		}
	}
	return false
}

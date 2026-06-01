package keyvault

import (
	"os"
	"testing"
)

func TestFileFallbackRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if v, ok := fileGet(KeyDeepSeek); ok {
		t.Fatalf("expected no key initially, got %q", v)
	}
	if err := fileSet(KeyDeepSeek, "sk-abc123"); err != nil {
		t.Fatal(err)
	}
	v, ok := fileGet(KeyDeepSeek)
	if !ok || v != "sk-abc123" {
		t.Errorf("fileGet = %q, %v; want sk-abc123, true", v, ok)
	}

	// File must be owner-only (0600).
	info, err := os.Stat(credFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("credentials.json perms = %o, want 600", perm)
	}

	fileDelete(KeyDeepSeek)
	if _, ok := fileGet(KeyDeepSeek); ok {
		t.Errorf("key should be deleted")
	}
}

func TestSetGetUsesFallbackWhenNoKeyring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	kr := NewKeyring(DefaultService)
	// On WSL/CI there's no Secret Service, so Set must persist via the file.
	if err := kr.Set(KeyDeepSeek, "sk-test"); err != nil {
		t.Fatalf("Set failed even with file fallback: %v", err)
	}
	got, err := kr.Get(KeyDeepSeek)
	if err != nil || got != "sk-test" {
		t.Errorf("Get = %q, %v; want sk-test", got, err)
	}
}

func TestHasAnyKeyFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lookup := func(k string) (string, bool) {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-x", true
		}
		return "", false
	}
	if !HasAnyKey(lookup) {
		t.Errorf("HasAnyKey should be true when DEEPSEEK_API_KEY is set")
	}
}

func TestEnvForDaemonInjectsStoredKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := fileSet(KeyDeepSeek, "sk-stored"); err != nil {
		t.Fatal(err)
	}
	// Env has nothing set, so the stored key should be injected.
	noEnv := func(string) (string, bool) { return "", false }
	env := EnvForDaemon(noEnv)
	found := false
	for _, e := range env {
		if e == "DEEPSEEK_API_KEY=sk-stored" {
			found = true
		}
	}
	if !found {
		t.Errorf("EnvForDaemon should inject DEEPSEEK_API_KEY from store; got %v", env)
	}

	// But an existing env var must win (not be overridden).
	withEnv := func(k string) (string, bool) {
		if k == "DEEPSEEK_API_KEY" {
			return "from-env", true
		}
		return "", false
	}
	for _, e := range EnvForDaemon(withEnv) {
		if e == "DEEPSEEK_API_KEY=sk-stored" {
			t.Errorf("env var should win over stored key")
		}
	}
}

package config

import (
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	// Try loading non-existent (should get defaults)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Failed to load defaults: %v", err)
	}
	if c.Profile != "fastapi-async" {
		t.Errorf("Expected default profile 'fastapi-async', got %s", c.Profile)
	}

	// Change and save
	c.Profile = "nextjs-app"
	if err := c.Save(path); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Load again
	c2, err := Load(path)
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}
	if c2.Profile != "nextjs-app" {
		t.Errorf("Expected 'nextjs-app' after reload, got %s", c2.Profile)
	}
}

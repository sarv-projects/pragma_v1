package config

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestLoadProfile(t *testing.T) {
	p, err := LoadProfile("fastapi-async")
	if err != nil {
		t.Fatalf("Failed to load fastapi-async profile: %v", err)
	}

	if p.Meta.Name != "FastAPI Async" {
		t.Errorf("Expected 'FastAPI Async', got %s", p.Meta.Name)
	}

	if p.Database.ORM != "sqlalchemy" {
		t.Errorf("Expected ORM 'sqlalchemy', got %s", p.Database.ORM)
	}
}

func TestListProfiles(t *testing.T) {
	metas := ListProfiles()
	if len(metas) == 0 {
		t.Errorf("Expected at least one profile, got 0")
	}

	found := false
	for _, m := range metas {
		if m.Language == "python" || m.Language == "typescript" || m.Language == "go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Did not find expected languages in profiles")
	}
}

func TestTomlTripleQuotes(t *testing.T) {
	tomlStr := `
[meta]
description = """
This is a multiline
description with triple quotes.
"""
`
	var p Profile
	err := toml.Unmarshal([]byte(tomlStr), &p)
	if err != nil {
		t.Fatalf("go-toml/v2 failed to parse triple quotes: %v", err)
	}
	expected := "This is a multiline\ndescription with triple quotes.\n"
	if p.Meta.Description != expected {
		t.Errorf("Expected description %q, got %q", expected, p.Meta.Description)
	}
}

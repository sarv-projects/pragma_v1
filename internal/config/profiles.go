package config

import (
	"embed"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed profiles/*.toml
var profilesFS embed.FS

type ProfileMeta struct {
	Name          string `toml:"name" json:"name"`
	Language      string `toml:"language" json:"language"`
	Version       string `toml:"version" json:"version"`
	Description   string `toml:"description" json:"description"`
	BeginnerLabel string `toml:"beginner_label" json:"beginner_label,omitempty"`
}

type FrameworkConfig struct {
	Name    string `toml:"name" json:"name"`
	Version string `toml:"version" json:"version"`
	Router  string `toml:"router" json:"router"`
}

type DatabaseConfig struct {
	ORM        string `toml:"orm" json:"orm"`
	ORMVersion string `toml:"orm_version" json:"orm_version"`
	Async      bool   `toml:"async" json:"async"`
	Migrations string `toml:"migrations" json:"migrations"`
	QueryGen   string `toml:"query_gen" json:"query_gen"`
	Driver     string `toml:"driver" json:"driver"`
}

type FrontendConfig struct {
	Enabled     bool   `toml:"enabled" json:"enabled"`
	Framework   string `toml:"framework" json:"framework"`
	Bundler     string `toml:"bundler" json:"bundler"`
	Language    string `toml:"language" json:"language"`
	Description string `toml:"description" json:"description"`
}

type TestingConfig struct {
	Framework string   `toml:"framework" json:"framework"`
	Plugins   []string `toml:"plugins" json:"plugins"`
	Tools     []string `toml:"tools" json:"tools"`
	Coverage  string   `toml:"coverage" json:"coverage"`
}

type DeploymentConfig struct {
	Dockerfile bool   `toml:"dockerfile" json:"dockerfile"`
	Compose    bool   `toml:"compose" json:"compose"`
	BaseImage  string `toml:"base_image" json:"base_image"`
	FinalImage string `toml:"final_image" json:"final_image"`
	Platform   string `toml:"platform" json:"platform"`
}

type LinterConfig struct {
	Tool   string `toml:"tool" json:"tool"`
	Config string `toml:"config" json:"config"`
}

type ContextBlock struct {
	Context string `toml:"context" json:"context"`
}

// Profile JSON tags MUST match the keys the Python daemon reads
// (meta, framework, database, testing, deployment, patterns, engineering,
// security, linter, conformance_rules). Without these the daemon received
// Go field names ("Meta", "PatternsBlock", ...) and treated every project as
// an empty python profile.
type Profile struct {
	Meta             ProfileMeta      `toml:"meta" json:"meta"`
	Framework        FrameworkConfig  `toml:"framework" json:"framework"`
	Database         DatabaseConfig   `toml:"database" json:"database"`
	Frontend         FrontendConfig   `toml:"frontend" json:"frontend"`
	Testing          TestingConfig    `toml:"testing" json:"testing"`
	Deployment       DeploymentConfig `toml:"deployment" json:"deployment"`
	PatternsBlock    ContextBlock     `toml:"patterns" json:"patterns"`
	EngineeringBlock ContextBlock     `toml:"engineering" json:"engineering"`
	SecurityBlock    ContextBlock     `toml:"security" json:"security"`
	Linter           LinterConfig     `toml:"linter" json:"linter"`
	Conformance      map[string]bool  `toml:"conformance_rules" json:"conformance_rules"`
}

// Patterns returns the context block
func (p *Profile) Patterns() string    { return p.PatternsBlock.Context }
func (p *Profile) Engineering() string { return p.EngineeringBlock.Context }
func (p *Profile) Security() string    { return p.SecurityBlock.Context }

func LoadProfile(name string) (*Profile, error) {
	data, err := profilesFS.ReadFile(fmt.Sprintf("profiles/%s.toml", name))
	if err != nil {
		return nil, fmt.Errorf("profile %q not found: %w", name, err)
	}

	var p Profile
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed to parse profile %q: %w", name, err)
	}

	return &p, nil
}

func ListProfiles() []ProfileMeta {
	entries, err := profilesFS.ReadDir("profiles")
	if err != nil {
		return nil
	}

	var metas []ProfileMeta
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".toml") {
			name := strings.TrimSuffix(e.Name(), ".toml")
			if p, err := LoadProfile(name); err == nil {
				metas = append(metas, p.Meta)
			}
		}
	}
	return metas
}

// ProfileNames returns the sorted list of embedded profile identifiers
// (file names without extension), suitable for selection UIs.
func ProfileNames() []string {
	entries, err := profilesFS.ReadDir("profiles")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	return names
}

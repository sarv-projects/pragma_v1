package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarv-projects/pragma/internal/config"
	"github.com/sarv-projects/pragma/internal/keyvault"
)

// TestHandleProfiles verifies that the profiles endpoint returns a non-empty list
func TestHandleProfiles_ReturnsProfiles(t *testing.T) {
	s := &Server{
		config: &config.Config{},
		kr:     keyvault.NewKeyring(keyvault.DefaultService),
	}

	req := httptest.NewRequest("GET", "/api/profiles", nil)
	w := httptest.NewRecorder()

	s.handleProfiles(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Define ProfileInfo locally for the test (matches the type in handlers.go)
	type ProfileInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		BeginnerLabel string `json:"beginner_label,omitempty"`
		Language      string `json:"language"`
	}

	var resp []ProfileInfo
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Should return at least some profiles
	if len(resp) == 0 {
		t.Error("Expected at least one profile, got empty list")
	}

	// Check that each profile has required fields
	for _, p := range resp {
		if p.ID == "" {
			t.Error("Found profile with empty ID")
		}
		if p.Name == "" {
			t.Error("Found profile with empty Name")
		}
	}
}

// TestProfileInfoJSON tests that ProfileInfo can be properly marshaled to JSON
func TestProfileInfoJSON(t *testing.T) {
	// Test the ProfileInfo struct used in handleProfiles
	type ProfileInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		BeginnerLabel string `json:"beginner_label,omitempty"`
		Language      string `json:"language"`
	}

	p := ProfileInfo{
		ID:            "fastapi-async",
		Name:          "FastAPI Async",
		Description:   "Async Python backend",
		BeginnerLabel: "Python + PostgreSQL",
		Language:      "python",
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Failed to marshal ProfileInfo: %v", err)
	}

	var decoded ProfileInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ProfileInfo: %v", err)
	}

	if decoded.ID != p.ID {
		t.Errorf("Expected ID %s, got %s", p.ID, decoded.ID)
	}
	if decoded.Name != p.Name {
		t.Errorf("Expected Name %s, got %s", p.Name, decoded.Name)
	}
	if decoded.Language != p.Language {
		t.Errorf("Expected Language %s, got %s", p.Language, decoded.Language)
	}
}

// TestHandleHealth verifies that the health endpoint returns expected structure
func TestHandleHealth_ReturnsExpectedStructure(t *testing.T) {
	// Create a temporary home directory for the test to isolate from real credentials
	tmpDir, err := os.MkdirTemp("", "pragma_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set HOME to tmpDir so the keyring uses a fresh credentials file
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create a fresh keyring that will use the temp directory
	// Note: We can't easily mock the keyring, but we can create a new one that will
	// use the temp HOME directory
	kr := keyvault.NewKeyring(keyvault.DefaultService)

	s := &Server{
		config: &config.Config{},
		kr:     kr,
	}

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	// Define HealthCheck locally for the test (matches the type in handlers.go)
	type HealthCheck struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	type HealthResponse struct {
		Checks map[string]HealthCheck `json:"checks"`
		IsWSL  bool                    `json:"is_wsl"`
		Port   string                  `json:"port"`
		AllOK  bool                    `json:"all_ok"`
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	// Verify expected checks are present
	expectedChecks := []string{"python", "daemon", "deepseek_key", "groq_key", "docker"}
	for _, checkName := range expectedChecks {
		if _, ok := resp.Checks[checkName]; !ok {
			t.Errorf("Expected check '%s' not found in health response", checkName)
		}
	}

	// Verify groq_key is OK:false when not configured (using fresh keyring with no keys)
	if groqCheck, ok := resp.Checks["groq_key"]; ok {
		if groqCheck.OK {
			t.Error("Expected groq_key OK to be false when not configured in fresh keyring, got true")
		}
	} else {
		t.Error("Expected groq_key check to be present")
	}

	// Verify port is set
	if resp.Port == "" {
		t.Error("Expected port to be non-empty")
	}
}

// TestHandleHealth_WithKeys verifies health check behavior with keys configured
func TestHandleHealth_WithKeys(t *testing.T) {
	// Create a temporary home directory for the test
	tmpDir, err := os.MkdirTemp("", "pragma_test_keys")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set HOME to tmpDir
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	// Create credentials file with a deepseek key (but no groq key)
	credsPath := filepath.Join(tmpDir, ".pragma", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(credsPath), 0700); err != nil {
		t.Fatalf("Failed to create .pragma dir: %v", err)
	}
	creds := map[string]string{"deepseek": "sk-test123"}
	credsJSON, _ := json.Marshal(creds)
	if err := os.WriteFile(credsPath, credsJSON, 0600); err != nil {
		t.Fatalf("Failed to write credentials: %v", err)
	}

	// Create a keyring that will use the temp directory
	kr := keyvault.NewKeyring(keyvault.DefaultService)

	s := &Server{
		config: &config.Config{},
		kr:     kr,
	}

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	checks, ok := resp["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected checks to be a map")
	}

	// Verify deepseek_key is OK:true (we configured it)
	if dsCheck, ok := checks["deepseek_key"].(map[string]interface{}); ok {
		if dsOK, ok := dsCheck["ok"].(bool); ok {
			if !dsOK {
				t.Error("Expected deepseek_key.ok to be true when configured")
			}
		} else {
			t.Error("Expected deepseek_key.ok to be a boolean")
		}
	} else {
		t.Error("Expected deepseek_key check to be present")
	}

	// Verify groq_key is OK:false (we didn't configure it)
	if groqCheck, ok := checks["groq_key"].(map[string]interface{}); ok {
		if groqOK, ok := groqCheck["ok"].(bool); ok {
			if groqOK {
				t.Error("Expected groq_key.ok to be false when not configured")
			}
		} else {
			t.Error("Expected groq_key.ok to be a boolean")
		}
	} else {
		t.Error("Expected groq_key check to be present")
	}
}

// TestHandleProfiles_FiltersInvalidProfiles verifies that invalid profiles are filtered out
func TestHandleProfiles_FiltersInvalidProfiles(t *testing.T) {
	s := &Server{
		config: &config.Config{},
		kr:     keyvault.NewKeyring(keyvault.DefaultService),
	}

	req := httptest.NewRequest("GET", "/api/profiles", nil)
	w := httptest.NewRecorder()

	s.handleProfiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	type ProfileInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		BeginnerLabel string `json:"beginner_label,omitempty"`
		Language      string `json:"language"`
	}

	var resp []ProfileInfo
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode profiles response: %v", err)
	}

	// All profiles should have non-empty IDs
	for _, p := range resp {
		if p.ID == "" {
			t.Error("Found profile with empty ID")
		}
		if p.Name == "" {
			t.Error("Found profile with empty Name")
		}
	}
}

// TestHandleProfiles_ReturnsBeginnerLabels verifies profiles have beginner_label field
func TestHandleProfiles_ReturnsBeginnerLabels(t *testing.T) {
	s := &Server{
		config: &config.Config{},
		kr:     keyvault.NewKeyring(keyvault.DefaultService),
	}

	req := httptest.NewRequest("GET", "/api/profiles", nil)
	w := httptest.NewRecorder()

	s.handleProfiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	type ProfileInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		BeginnerLabel string `json:"beginner_label,omitempty"`
		Language      string `json:"language"`
	}

	var resp []ProfileInfo
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode profiles response: %v", err)
	}

	// Check that at least some profiles have beginner labels
	// This verifies the fix for P0 Profile picker bug
	hasBeginnerLabel := false
	for _, p := range resp {
		if p.BeginnerLabel != "" {
			hasBeginnerLabel = true
			break
		}
	}

	if !hasBeginnerLabel && len(resp) > 0 {
		t.Error("Expected at least one profile to have a beginner_label")
	}
}

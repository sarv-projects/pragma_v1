package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// allowedBaseURLs is the set of known-safe provider base URLs.
// Custom/unknown URLs are blocked to prevent SSRF attacks.
var allowedBaseURLs = map[string]bool{
	"https://api.deepseek.com":            true,
	"https://api.openai.com/v1":           true,
	"https://api.groq.com/openai/v1":      true,
	"https://api.together.xyz/v1":         true,
	"https://openrouter.ai/api/v1":        true,
	"https://api.anthropic.com/v1":        true,
	"http://localhost:11434":               true, // Ollama default
	"http://127.0.0.1:11434":              true, // Ollama default
}

// ValidateAPIKey makes a request to the provider's models endpoint to verify
// the key is valid. Returns nil on success, a descriptive error otherwise.
func ValidateAPIKey(baseURL, apiKey, provider string) ([]string, error) {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}

	// Normalize URL
	baseURL = strings.TrimRight(baseURL, "/")

	// SSRF protection: only allow known provider URLs or localhost for custom/Ollama
	if !allowedBaseURLs[baseURL] {
		// Allow localhost URLs for custom/Ollama providers
		if strings.HasPrefix(baseURL, "http://localhost:") || strings.HasPrefix(baseURL, "http://127.0.0.1:") {
			// Allowed — local provider
		} else {
			return nil, fmt.Errorf("base URL %q is not in the allowed provider list. Use a known provider or localhost", baseURL)
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// For Ollama, try /v1/models first, then fall back to /api/tags
	if strings.ToLower(provider) == "ollama" {
		models, err := tryModelsEndpoint(client, baseURL+"/v1/models", apiKey)
		if err == nil {
			return models, nil
		}
		// Fallback to Ollama-native /api/tags endpoint
		return tryOllamaEndpoint(client, baseURL+"/api/tags", apiKey)
	}

	return tryModelsEndpoint(client, baseURL+"/models", apiKey)
}

// tryModelsEndpoint attempts to fetch models from an OpenAI-compatible /models endpoint.
func tryModelsEndpoint(client *http.Client, modelsURL, apiKey string) ([]string, error) {
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("authentication failed (HTTP %d): invalid API key", resp.StatusCode)
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("endpoint not found (HTTP 404)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected response (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	// Try to parse model list (OpenAI format: { "data": [...] })
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	models := []string{}
	if err := json.Unmarshal(body, &result); err == nil {
		for _, m := range result.Data {
			models = append(models, m.ID)
		}
	}

	return models, nil
}

// tryOllamaEndpoint attempts to fetch models from Ollama's native /api/tags endpoint.
func tryOllamaEndpoint(client *http.Client, tagsURL, apiKey string) ([]string, error) {
	req, err := http.NewRequest("GET", tagsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("authentication failed (HTTP %d): invalid API key", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected response (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	// Ollama format: { "models": [{ "name": "..." }, ...] }
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	models := []string{}
	if err := json.Unmarshal(body, &result); err == nil {
		for _, m := range result.Models {
			models = append(models, m.Name)
		}
	}

	return models, nil
}

func (s *Server) handleValidateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.APIKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":  false,
			"error":  "API key is required",
			"models": []string{},
		})
		return
	}

	// Determine base URL from provider
	baseURL := req.BaseURL
	if baseURL == "" {
		switch strings.ToLower(req.Provider) {
		case "deepseek":
			baseURL = "https://api.deepseek.com"
		case "openai":
			baseURL = "https://api.openai.com/v1"
		case "groq":
			baseURL = "https://api.groq.com/openai/v1"
		case "together":
			baseURL = "https://api.together.xyz/v1"
		}
	}

	models, err := ValidateAPIKey(baseURL, req.APIKey, req.Provider)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":  false,
			"error":  err.Error(),
			"models": []string{},
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid":  true,
		"error":  "",
		"models": models,
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

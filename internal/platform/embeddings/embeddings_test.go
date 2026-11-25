package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewProvider_DisabledAndValidation(t *testing.T) {
	p, err := NewProvider(Config{})
	if err != nil || p != nil {
		t.Fatalf("expected nil provider when disabled, got (%v, %v)", p, err)
	}

	p, err = NewProvider(Config{Provider: "none"})
	if err != nil || p != nil {
		t.Fatalf("expected nil provider for none, got (%v, %v)", p, err)
	}

	if _, err := NewProvider(Config{Provider: "openai"}); err == nil {
		t.Fatalf("expected error when openai API key is missing")
	}

	if _, err := NewProvider(Config{Provider: "gemini"}); err == nil {
		t.Fatalf("expected error when gemini API key is missing")
	}

	if _, err := NewProvider(Config{Provider: "openai-compatible"}); err == nil {
		t.Fatalf("expected error when openai-compatible base URL is missing")
	}

	if _, err := NewProvider(Config{Provider: "unknown"}); err == nil {
		t.Fatalf("expected error for unsupported provider")
	}
}

func TestOpenAIProvider_EmbedSuccessAndDimMismatch(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		vec := make([]float32, 4)
		vec[0] = 0.25
		vec[1] = 0.5
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": vec},
			},
		})
	}))
	defer srv.Close()

	p, err := NewProvider(Config{
		Provider:   "openai",
		APIKey:     "sk-test-123",
		BaseURL:    srv.URL + "/v1",
		Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	vec, err := p.Embed(context.Background(), "hello engram")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 4 || vec[0] != 0.25 {
		t.Fatalf("unexpected vector: %v", vec)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if gotPath != "/v1/embeddings" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if gotBody["model"] != "text-embedding-3-small" {
		t.Fatalf("unexpected model: %v", gotBody["model"])
	}

	// Dimension mismatch check
	pWrongDim, err := NewProvider(Config{
		Provider:   "openai",
		APIKey:     "sk-test-123",
		BaseURL:    srv.URL + "/v1",
		Dimensions: 1536,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	if _, err := pWrongDim.Embed(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
		t.Fatalf("expected dimension mismatch error, got %v", err)
	}
}

func TestGeminiProvider_EmbedSuccessAndError(t *testing.T) {
	var gotKey string
	var gotPath string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		if strings.Contains(gotPath, "fail-model") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}

		vec := make([]float32, 4)
		vec[2] = 0.75
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": map[string]interface{}{
				"values": vec,
			},
		})
	}))
	defer srv.Close()

	p, err := NewProvider(Config{
		Provider:   "gemini",
		APIKey:     "gem-key-xyz",
		Model:      "text-embedding-004",
		BaseURL:    srv.URL + "/v1beta",
		Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	vec, err := p.Embed(context.Background(), "semantic search query")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 4 || vec[2] != 0.75 {
		t.Fatalf("unexpected vector: %v", vec)
	}
	if gotKey != "gem-key-xyz" {
		t.Fatalf("unexpected x-goog-api-key header: %q", gotKey)
	}
	if gotPath != "/v1beta/models/text-embedding-004:embedContent" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if gotBody["outputDimensionality"] != float64(4) {
		t.Fatalf("unexpected outputDimensionality: %v", gotBody["outputDimensionality"])
	}

	pFail, err := NewProvider(Config{
		Provider:   "gemini",
		APIKey:     "bad",
		Model:      "fail-model",
		BaseURL:    srv.URL + "/v1beta",
		Dimensions: 4,
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	if _, err := pFail.Embed(context.Background(), "query"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 API error, got %v", err)
	}
}

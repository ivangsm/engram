package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultDimensions = 1536
	maxInputBytes     = 24000
)

// Provider generates vector embeddings for semantic memory storage and search.
type Provider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Name() string
}

// Config configures an optional embedding provider.
type Config struct {
	Provider   string
	APIKey     string
	Model      string
	BaseURL    string
	Dimensions int
	Timeout    time.Duration
}

// LoadConfigFromEnv reads embedding configuration from environment variables.
func LoadConfigFromEnv() Config {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDING_PROVIDER")))
	apiKey := strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDING_API_KEY"))
	if apiKey == "" {
		switch provider {
		case "openai":
			apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		case "gemini":
			apiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
			if apiKey == "" {
				apiKey = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
			}
		}
	}

	dims := DefaultDimensions
	if raw := strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDING_DIMENSIONS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			dims = parsed
		}
	}

	return Config{
		Provider:   provider,
		APIKey:     apiKey,
		Model:      strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDING_MODEL")),
		BaseURL:    strings.TrimSpace(os.Getenv("ENGRAM_EMBEDDING_BASE_URL")),
		Dimensions: dims,
		Timeout:    15 * time.Second,
	}
}

// NewFromEnv builds an embedding Provider from environment variables.
// Returns (nil, nil) when no provider is configured or provider is "none".
func NewFromEnv() (Provider, error) {
	return NewProvider(LoadConfigFromEnv())
}

// NewProvider validates cfg and constructs the requested embedding Provider.
func NewProvider(cfg Config) (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" || provider == "none" || provider == "disabled" {
		return nil, nil
	}

	dims := cfg.Dimensions
	if dims <= 0 {
		dims = DefaultDimensions
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	switch provider {
	case "openai":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, errors.New("ENGRAM_EMBEDDING_API_KEY (or OPENAI_API_KEY) is required when ENGRAM_EMBEDDING_PROVIDER=openai")
		}
		model := cfg.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return &openAIProvider{
			name:       "openai",
			apiKey:     cfg.APIKey,
			model:      model,
			baseURL:    strings.TrimRight(baseURL, "/"),
			dimensions: dims,
			client:     client,
		}, nil

	case "openai-compatible":
		if strings.TrimSpace(cfg.BaseURL) == "" {
			return nil, errors.New("ENGRAM_EMBEDDING_BASE_URL is required when ENGRAM_EMBEDDING_PROVIDER=openai-compatible")
		}
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, errors.New("ENGRAM_EMBEDDING_MODEL is required when ENGRAM_EMBEDDING_PROVIDER=openai-compatible")
		}
		return &openAIProvider{
			name:       "openai-compatible",
			apiKey:     cfg.APIKey,
			model:      cfg.Model,
			baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
			dimensions: dims,
			client:     client,
		}, nil

	case "gemini":
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, errors.New("ENGRAM_EMBEDDING_API_KEY (or GEMINI_API_KEY) is required when ENGRAM_EMBEDDING_PROVIDER=gemini")
		}
		model := cfg.Model
		if model == "" {
			model = "gemini-embedding-001"
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
		return &geminiProvider{
			apiKey:     cfg.APIKey,
			model:      strings.TrimPrefix(model, "models/"),
			baseURL:    strings.TrimRight(baseURL, "/"),
			dimensions: dims,
			client:     client,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported ENGRAM_EMBEDDING_PROVIDER %q (supported: none, openai, openai-compatible, gemini)", cfg.Provider)
	}
}

func truncateUTF8(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

type openAIProvider struct {
	name       string
	apiKey     string
	model      string
	baseURL    string
	dimensions int
	client     *http.Client
}

func (p *openAIProvider) Name() string {
	return fmt.Sprintf("%s:%s", p.name, p.model)
}

func (p *openAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	input := truncateUTF8(text, maxInputBytes)
	if input == "" {
		return nil, errors.New("cannot generate embedding for empty text")
	}

	reqBody := map[string]interface{}{
		"model": p.model,
		"input": input,
	}
	if p.dimensions > 0 && p.model != "text-embedding-ada-002" && p.name == "openai" {
		reqBody["dimensions"] = p.dimensions
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal openai embedding request: %w", err)
	}

	endpoint := p.baseURL
	if !strings.HasSuffix(endpoint, "/embeddings") {
		endpoint += "/embeddings"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create openai embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read openai embedding response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai embedding API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode openai embedding response: %w", err)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, errors.New("openai embedding response contained no vector data")
	}

	vec := parsed.Data[0].Embedding
	if p.dimensions > 0 && len(vec) != p.dimensions {
		return nil, fmt.Errorf("embedding dimension mismatch: got %d, expected %d", len(vec), p.dimensions)
	}
	return vec, nil
}

type geminiProvider struct {
	apiKey     string
	model      string
	baseURL    string
	dimensions int
	client     *http.Client
}

func (p *geminiProvider) Name() string {
	return fmt.Sprintf("gemini:%s", p.model)
}

func (p *geminiProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	input := truncateUTF8(text, maxInputBytes)
	if input == "" {
		return nil, errors.New("cannot generate embedding for empty text")
	}

	reqBody := map[string]interface{}{
		"model": "models/" + p.model,
		"content": map[string]interface{}{
			"parts": []map[string]string{
				{"text": input},
			},
		},
	}
	if p.dimensions > 0 {
		reqBody["outputDimensionality"] = p.dimensions
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini embedding request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/models/%s:embedContent", p.baseURL, p.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create gemini embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read gemini embedding response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini embedding API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode gemini embedding response: %w", err)
	}
	if len(parsed.Embedding.Values) == 0 {
		return nil, errors.New("gemini embedding response contained no vector values")
	}

	vec := parsed.Embedding.Values
	if p.dimensions > 0 && len(vec) != p.dimensions {
		return nil, fmt.Errorf("embedding dimension mismatch: got %d, expected %d", len(vec), p.dimensions)
	}
	return vec, nil
}

package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"time"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
)

// EmbeddingProvider defines the interface for generating text embeddings.
// This allows swapping between OpenAI, local models, and mock implementations.
type EmbeddingProvider interface {
	// GenerateEmbedding creates a vector embedding for the given input text.
	// Returns a slice of float32 values representing the embedding vector.
	GenerateEmbedding(ctx context.Context, input string) (model.Vector, error)
}

// EmbeddingService manages embedding generation and storage for catalog SKUs.
// It wraps an EmbeddingProvider and a repository for database persistence.
type EmbeddingService struct {
	Provider EmbeddingProvider    // exported for direct access by HybridSearchService
	repo     EmbeddingRepository
}

// EmbeddingRepository is the interface for embedding persistence operations.
// Defined here to avoid circular imports with the repository package.
type EmbeddingRepository interface {
	UpdateEmbedding(ctx context.Context, skuID string, embedding model.Vector) error
	SearchByVector(ctx context.Context, embedding model.Vector, limit int) ([]model.SKU, error)
	HybridSearch(ctx context.Context, query string, embedding model.Vector, limit int) ([]model.HybridSearchResult, error)
}

// NewEmbeddingService creates a new EmbeddingService.
func NewEmbeddingService(provider EmbeddingProvider, repo EmbeddingRepository) *EmbeddingService {
	return &EmbeddingService{
		provider: provider,
		repo:     repo,
	}
}

// buildSKUInput constructs the text input for embedding generation from a SKU.
// Combines title, description, and structured specs into a single text block.
func buildSKUInput(sku *model.SKU) string {
	var buf bytes.Buffer

	buf.WriteString("Product: ")
	buf.WriteString(sku.Title)
	buf.WriteString("\n")

	if sku.Description != nil && *sku.Description != "" {
		buf.WriteString("Description: ")
		buf.WriteString(*sku.Description)
		buf.WriteString("\n")
	}

	buf.WriteString("SKU ID: ")
	buf.WriteString(sku.SkuID)
	buf.WriteString("\n")

	// Parse specs JSON for structured fields
	if len(sku.Specs) > 0 && string(sku.Specs) != "{}" {
		var specs map[string]interface{}
		if err := json.Unmarshal(sku.Specs, &specs); err == nil {
			buf.WriteString("Specifications:\n")
			for k, v := range specs {
				buf.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
			}
		}
	}

	return buf.String()
}

// GenerateEmbeddingForSKU generates an embedding for a single SKU and returns it.
// The caller is responsible for persisting the embedding via the repository.
func (s *EmbeddingService) GenerateEmbeddingForSKU(ctx context.Context, sku *model.SKU) (model.Vector, error) {
	input := buildSKUInput(sku)
	embedding, err := s.Provider.GenerateEmbedding(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("embedding_service: generate for SKU %s: %w", sku.SkuID, err)
	}
	return embedding, nil
}

// GenerateAndStore generates an embedding for a SKU and persists it to the database.
func (s *EmbeddingService) GenerateAndStore(ctx context.Context, sku *model.SKU) error {
	if s.repo == nil {
		return fmt.Errorf("embedding_service: no repository configured")
	}
	embedding, err := s.GenerateEmbeddingForSKU(ctx, sku)
	if err != nil {
		return err
	}

	if err := s.repo.UpdateEmbedding(ctx, sku.SkuID, embedding); err != nil {
		return fmt.Errorf("embedding_service: store for SKU %s: %w", sku.SkuID, err)
	}

	return nil
}

// BatchGenerate generates and stores embeddings for multiple SKUs.
// Each SKU is processed independently; failures on individual SKUs are collected
// and returned as a combined error, while successful embeddings are persisted.
func (s *EmbeddingService) BatchGenerate(ctx context.Context, skus []*model.SKU) error {
	var errs []error

	for _, sku := range skus {
		if err := s.GenerateAndStore(ctx, sku); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("embedding_service: batch generate: %d/%d SKUs failed: %v",
			len(errs), len(skus), errs)
	}

	return nil
}

// VectorSearch performs a pure vector similarity search.
func (s *EmbeddingService) VectorSearch(ctx context.Context, queryText string, limit int) ([]model.SKU, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("embedding_service: no repository configured for vector search")
	}
	embedding, err := s.Provider.GenerateEmbedding(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("embedding_service: vector search: generate query embedding: %w", err)
	}

	skus, err := s.repo.SearchByVector(ctx, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("embedding_service: vector search: %w", err)
	}

	return skus, nil
}

// HybridSearch performs a combined FTS + vector search using Reciprocal Rank Fusion.
// Returns model.HybridSearchResult which wraps SKU with similarity and method metadata.
func (s *EmbeddingService) HybridSearch(ctx context.Context, queryText string, limit int) ([]model.HybridSearchResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("embedding_service: no repository configured for hybrid search")
	}
	embedding, err := s.Provider.GenerateEmbedding(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("embedding_service: hybrid search: generate query embedding: %w", err)
	}

	results, err := s.repo.HybridSearch(ctx, queryText, embedding, limit)
	if err != nil {
		return nil, fmt.Errorf("embedding_service: hybrid search: %w", err)
	}

	return results, nil
}

// CosineSimilarity computes the cosine similarity between two float32 vectors.
// Returns a value in [-1, 1] where 1 means identical direction.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// =============================================================================
// OpenAI Embedding Provider
// =============================================================================

// OpenAIEmbeddingProvider generates embeddings using the OpenAI API.
// Compatible with text-embedding-3-small (1536-dim) and text-embedding-3-large (3072-dim).
type OpenAIEmbeddingProvider struct {
	apiKey     string
	endpoint   string
	model      string
	httpClient *http.Client
}

// OpenAIEmbeddingConfig holds configuration for the OpenAI embedding provider.
type OpenAIEmbeddingConfig struct {
	APIKey   string
	Endpoint string // defaults to "https://api.openai.com/v1/embeddings"
	Model    string // defaults to "text-embedding-3-small"
}

// NewOpenAIEmbeddingProvider creates a new OpenAI embedding provider.
func NewOpenAIEmbeddingProvider(cfg OpenAIEmbeddingConfig) *OpenAIEmbeddingProvider {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.openai.com/v1/embeddings"
	}
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}

	return &OpenAIEmbeddingProvider{
		apiKey:     cfg.APIKey,
		endpoint:   cfg.Endpoint,
		model:      cfg.Model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// openAIEmbeddingRequest 是调用 OpenAI 嵌入接口的请求体。
type openAIEmbeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

// openAIEmbeddingResponse 是 OpenAI 嵌入接口的响应体，包含嵌入数据或错误信息。
type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// GenerateEmbedding calls the OpenAI embeddings API and returns the vector.
func (p *OpenAIEmbeddingProvider) GenerateEmbedding(ctx context.Context, input string) (model.Vector, error) {
	reqBody := openAIEmbeddingRequest{
		Input: input,
		Model: p.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: http request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var embResp openAIEmbeddingResponse
	if err := json.Unmarshal(respBytes, &embResp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal response: %w", err)
	}

	if embResp.Error != nil {
		return nil, fmt.Errorf("openai: API error: %s (%s)", embResp.Error.Message, embResp.Error.Type)
	}

	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("openai: no embedding data in response")
	}

	// Convert float64 to float32
	vec := make(model.Vector, len(embResp.Data[0].Embedding))
	for i, v := range embResp.Data[0].Embedding {
		vec[i] = float32(v)
	}

	return vec, nil
}

// =============================================================================
// Mock Embedding Provider (for local development without API key)
// =============================================================================

// MockEmbeddingProvider generates deterministic pseudo-random embeddings for testing.
// It uses a hash of the input text to seed the random vector, ensuring the same
// input always produces the same embedding (within a single process lifetime).
// The vectors are 1536-dimensional, compatible with OpenAI text-embedding-3-small.
type MockEmbeddingProvider struct {
	dimension int
}

// NewMockEmbeddingProvider creates a new mock embedding provider.
// Default dimension is 1536 (matching text-embedding-3-small).
func NewMockEmbeddingProvider() *MockEmbeddingProvider {
	return &MockEmbeddingProvider{dimension: 1536}
}

// NewMockEmbeddingProviderWithDim creates a mock provider with a custom dimension.
func NewMockEmbeddingProviderWithDim(dimension int) *MockEmbeddingProvider {
	return &MockEmbeddingProvider{dimension: dimension}
}

// xorshift64Star is a fast deterministic PRNG.
// See: "An experimental exploration of Marsaglia's xorshift generators, scrambled"
func xorshift64Star(state *uint64) uint64 {
	*state ^= *state >> 12
	*state ^= *state << 25
	*state ^= *state >> 27
	return *state * 2685821657736338717
}

// GenerateEmbedding returns a pseudo-random normalized vector derived from the input text.
// The output is deterministic for the same input within a single process.
// Vectors are L2-normalized for cosine similarity compatibility.
func (m *MockEmbeddingProvider) GenerateEmbedding(ctx context.Context, input string) (model.Vector, error) {
	// Seed the PRNG state from the input hash using FNV-1a-64.
	fnvPrime := uint64(1099511628211)
	state := uint64(14695981039346656037) // FNV offset basis
	for _, b := range []byte(input) {
		state ^= uint64(b)
		state *= fnvPrime
	}
	if state == 0 {
		state = 1 // XorShift requires non-zero state
	}

	vec := make(model.Vector, m.dimension)
	var sumSquares float64

	for i := 0; i < m.dimension; i++ {
		// Generate 53-bit float in [0, 1) then map to [-1, 1).
		next := xorshift64Star(&state)
		f := float64(next>>11) / float64(1<<53)
		val := float32(f*2.0 - 1.0)
		vec[i] = val
		sumSquares += float64(val) * float64(val)
	}

	// L2-normalize so cosine similarity works correctly.
	if sumSquares > 0 {
		norm := float32(1.0 / math.Sqrt(sumSquares))
		for i := range vec {
			vec[i] *= norm
		}
	}

	return vec, nil
}

// =============================================================================
// Compile-time interface checks
// =============================================================================
var _ EmbeddingProvider = (*OpenAIEmbeddingProvider)(nil)
var _ EmbeddingProvider = (*MockEmbeddingProvider)(nil)

// =============================================================================
// Prevent unused import errors for crypto/rand and math/big
// =============================================================================
var _ = rand.Reader
var _ = big.NewInt

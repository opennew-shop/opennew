package service

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/catalog/internal/service"
	"github.com/ancf-commerce/ancf/services/catalog/repository"
)

type CatalogService = internal.CatalogService
type SearchResult = internal.SearchResult
type SearchMode = internal.SearchMode
type HybridSearchResult = internal.HybridSearchResult
type HybridSearchService = internal.HybridSearchService
type EmbeddingService = internal.EmbeddingService
type EmbeddingProvider = internal.EmbeddingProvider
type EmbeddingRepository = internal.EmbeddingRepository
type OpenAIEmbeddingConfig = internal.OpenAIEmbeddingConfig
type OpenAIEmbeddingProvider = internal.OpenAIEmbeddingProvider
type MockEmbeddingProvider = internal.MockEmbeddingProvider
type RAGService = internal.RAGService
type AgentSearchResponse = internal.AgentSearchResponse

const (
	SearchModeHybrid  = internal.SearchModeHybrid
	SearchModeKeyword = internal.SearchModeKeyword
	SearchModeVector  = internal.SearchModeVector
)

func NewCatalogService(db *sql.DB, repo *repository.SKURepository) *CatalogService {
	return internal.NewCatalogService(db, repo)
}

func ParseSearchMode(s string) SearchMode {
	return internal.ParseSearchMode(s)
}

func NewEmbeddingService(provider EmbeddingProvider, repo EmbeddingRepository) *EmbeddingService {
	return internal.NewEmbeddingService(provider, repo)
}

func NewOpenAIEmbeddingProvider(cfg OpenAIEmbeddingConfig) *OpenAIEmbeddingProvider {
	return internal.NewOpenAIEmbeddingProvider(cfg)
}

func NewMockEmbeddingProvider() *MockEmbeddingProvider {
	return internal.NewMockEmbeddingProvider()
}

func NewMockEmbeddingProviderWithDim(dimension int) *MockEmbeddingProvider {
	return internal.NewMockEmbeddingProviderWithDim(dimension)
}

func NewHybridSearchService(keywordSearch *CatalogService, embeddingSvc *EmbeddingService) *HybridSearchService {
	return internal.NewHybridSearchService(keywordSearch, embeddingSvc)
}

func ToLegacyResult(results []HybridSearchResult, total int, limit, offset int) *SearchResult {
	return internal.ToLegacyResult(results, total, limit, offset)
}

func NewRAGService(hybridSearch *HybridSearchService) *RAGService {
	return internal.NewRAGService(hybridSearch)
}

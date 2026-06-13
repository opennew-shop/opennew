package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
)

// SearchMode defines the search strategy.
type SearchMode string

const (
	// SearchModeHybrid uses RRF fusion of keyword + vector (default).
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeKeyword uses PostgreSQL FTS only.
	SearchModeKeyword SearchMode = "keyword"
	// SearchModeVector uses vector semantic similarity only.
	SearchModeVector SearchMode = "vector"
)

// ParseSearchMode converts a string to a SearchMode, defaulting to hybrid.
func ParseSearchMode(s string) SearchMode {
	switch s {
	case "keyword", "fts":
		return SearchModeKeyword
	case "vector", "semantic":
		return SearchModeVector
	default:
		return SearchModeHybrid
	}
}

// HybridSearchResult represents a single result from hybrid (RRF-fused) search
// returned to API clients. Uses SKUSearchResult (display-only) for backward compatibility.
type HybridSearchResult struct {
	SKU         model.SKUSearchResult `json:"sku"`
	Score       float64               `json:"score"`
	KeywordRank int                   `json:"keyword_rank"`
	VectorRank  int                   `json:"vector_rank"`
	Source      string                `json:"source"` // "fts" | "vector" | "hybrid"
}

// HybridSearchService performs mixed search: FTS keyword + vector semantic -> RRF fusion.
// It is the top-level search orchestrator for the catalog service.
type HybridSearchService struct {
	keywordSearch *CatalogService
	embeddingSvc  *EmbeddingService
	rrfK          int // RRF rank constant, default 60
}

// NewHybridSearchService creates a new HybridSearchService.
// embeddingSvc may be nil if vector search is not configured.
func NewHybridSearchService(keywordSearch *CatalogService, embeddingSvc *EmbeddingService) *HybridSearchService {
	return &HybridSearchService{
		keywordSearch: keywordSearch,
		embeddingSvc:  embeddingSvc,
		rrfK:          60,
	}
}

// SetRRFK configures the Reciprocal Rank Fusion constant.
func (s *HybridSearchService) SetRRFK(k int) {
	s.rrfK = k
}

// IsVectorAvailable returns whether vector search is configured and available.
func (s *HybridSearchService) IsVectorAvailable() bool {
	return s.embeddingSvc != nil
}

// Search executes a search with the given mode and returns hybrid search results.
func (s *HybridSearchService) Search(ctx context.Context, query string, limit int, mode SearchMode) ([]HybridSearchResult, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}

	switch mode {
	case SearchModeKeyword:
		return s.keywordOnly(ctx, query, limit)
	case SearchModeVector:
		return s.vectorOnly(ctx, query, limit)
	default:
		return s.hybridFusion(ctx, query, limit)
	}
}

// keywordOnly performs a pure FTS keyword search.
func (s *HybridSearchService) keywordOnly(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	searchResult, err := s.keywordSearch.Search(ctx, query, limit, 0)
	if err != nil {
		return nil, fmt.Errorf("hybrid_search: keyword-only: %w", err)
	}

	results := make([]HybridSearchResult, 0, len(searchResult.Items))
	for i, item := range searchResult.Items {
		results = append(results, HybridSearchResult{
			SKU:         item,
			Score:       1.0 - float64(i)*0.01,
			KeywordRank: i + 1,
			VectorRank:  0,
			Source:      "fts",
		})
	}
	return results, nil
}

// vectorOnly performs a pure vector semantic search.
// Falls back to keyword-only if vector is unavailable.
func (s *HybridSearchService) vectorOnly(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	if s.embeddingSvc == nil {
		return s.keywordOnly(ctx, query, limit)
	}

	skus, err := s.embeddingSvc.VectorSearch(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("hybrid_search: vector-only: %w", err)
	}

	results := make([]HybridSearchResult, 0, len(skus))
	for i, sku := range skus {
		results = append(results, HybridSearchResult{
			SKU:         skuToSearchResult(sku),
			Score:       1.0 - float64(i)*0.01,
			KeywordRank: 0,
			VectorRank:  i + 1,
			Source:      "vector",
		})
	}
	return results, nil
}

// hybridFusion performs parallel keyword + vector search with RRF fusion.
func (s *HybridSearchService) hybridFusion(ctx context.Context, query string, limit int) ([]HybridSearchResult, error) {
	// Fetch all candidates for vector search if needed.
	fullResult, err := s.keywordSearch.Search(ctx, "", 100, 0)
	if err != nil {
		return nil, fmt.Errorf("hybrid_search: fetch candidates: %w", err)
	}

	var (
		keywordResults []model.SKUSearchResult
		vectorResults  []model.SKUSearchResult
		wg             sync.WaitGroup
		keywordErr     error
		vectorErr      error
	)

	// Keyword search goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sr, err := s.keywordSearch.Search(ctx, query, 50, 0)
		if err != nil {
			keywordErr = err
			return
		}
		keywordResults = sr.Items
	}()

	// Vector search goroutine (only if embedding service available).
	if s.embeddingSvc != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// For the vector search, we need embeddings for all candidates.
			// In mock mode, we compute cosine similarity between query and each candidate.
			provider := s.embeddingSvc.Provider
			if provider == nil {
				return
			}
			queryEmbedding, err := provider.GenerateEmbedding(ctx, query)
			if err != nil {
				vectorErr = err
				return
			}
			// Compute similarity with each candidate.
			type scoredSKU struct {
				sku  model.SKUSearchResult
				sim  float64
			}
			var scored []scoredSKU
			for _, item := range fullResult.Items {
				itemText := buildSKUSearchText(item)
				itemEmbedding, err := provider.GenerateEmbedding(ctx, itemText)
				if err != nil {
					continue
				}
				sim := CosineSimilarity(queryEmbedding, itemEmbedding)
				scored = append(scored, scoredSKU{sku: item, sim: sim})
			}
			sort.Slice(scored, func(i, j int) bool {
				return scored[i].sim > scored[j].sim
			})
			for _, s := range scored {
				vectorResults = append(vectorResults, s.sku)
			}
		}()
	}
	wg.Wait()

	if keywordErr != nil {
		return nil, fmt.Errorf("hybrid_search: keyword search failed: %w", keywordErr)
	}
	if vectorErr != nil {
		// Vector search failed, fall back to keyword-only.
		return s.resultFromKeyword(keywordResults, limit), nil
	}

	if s.embeddingSvc == nil || len(vectorResults) == 0 {
		return s.resultFromKeyword(keywordResults, limit), nil
	}

	// RRF fusion.
	merged := rrfFusion(keywordResults, vectorResults, s.rrfK, limit)
	return merged, nil
}

// resultFromKeyword wraps keyword results as HybridSearchResult.
func (s *HybridSearchService) resultFromKeyword(items []model.SKUSearchResult, limit int) []HybridSearchResult {
	results := make([]HybridSearchResult, 0, limit)
	for i, item := range items {
		if i >= limit {
			break
		}
		results = append(results, HybridSearchResult{
			SKU:         item,
			Score:       1.0 - float64(i)*0.01,
			KeywordRank: i + 1,
			VectorRank:  0,
			Source:      "fts",
		})
	}
	return results
}

// rrfFusion performs Reciprocal Rank Fusion on two ranked result lists.
func rrfFusion(
	keywordResults []model.SKUSearchResult,
	vectorResults []model.SKUSearchResult,
	k int,
	topN int,
) []HybridSearchResult {
	keywordRank := make(map[string]int, len(keywordResults))
	for i, item := range keywordResults {
		keywordRank[item.SkuID] = i + 1
	}

	vectorRank := make(map[string]int, len(vectorResults))
	for i, item := range vectorResults {
		vectorRank[item.SkuID] = i + 1
	}

	defaultKeywordRank := len(keywordResults) + 1
	defaultVectorRank := len(vectorResults) + 1

	allSKUs := make(map[string]model.SKUSearchResult)
	for _, item := range keywordResults {
		allSKUs[item.SkuID] = item
	}
	for _, item := range vectorResults {
		allSKUs[item.SkuID] = item
	}

	type scored struct {
		skuID       string
		sku         model.SKUSearchResult
		rrfScore    float64
		keywordRank int
		vectorRank  int
	}

	scoredList := make([]scored, 0, len(allSKUs))
	for skuID, sku := range allSKUs {
		kr := keywordRank[skuID]
		if kr == 0 {
			kr = defaultKeywordRank
		}
		vr := vectorRank[skuID]
		if vr == 0 {
			vr = defaultVectorRank
		}

		rrfScore := (1.0 / float64(k+kr)) + (1.0 / float64(k+vr))

		scoredList = append(scoredList, scored{
			skuID:       skuID,
			sku:         sku,
			rrfScore:    rrfScore,
			keywordRank: keywordRank[skuID],
			vectorRank:  vectorRank[skuID],
		})
	}

	sort.Slice(scoredList, func(i, j int) bool {
		return scoredList[i].rrfScore > scoredList[j].rrfScore
	})

	limit := int(math.Min(float64(topN), float64(len(scoredList))))
	results := make([]HybridSearchResult, 0, limit)
	for i := 0; i < limit; i++ {
		s := scoredList[i]
		source := "hybrid"
		if s.keywordRank > 0 && s.vectorRank == 0 {
			source = "fts"
		} else if s.keywordRank == 0 && s.vectorRank > 0 {
			source = "vector"
		}
		results = append(results, HybridSearchResult{
			SKU:         s.sku,
			Score:       math.Round(s.rrfScore*10000) / 10000,
			KeywordRank: s.keywordRank,
			VectorRank:  s.vectorRank,
			Source:      source,
		})
	}
	return results
}

// skuToSearchResult converts a model.SKU to a model.SKUSearchResult.
func skuToSearchResult(sku model.SKU) model.SKUSearchResult {
	return model.SKUSearchResult{
		SkuID: sku.SkuID,
		Title: sku.Title,
		Price: model.SKUPriceDisplay{
			Currency:    sku.Currency,
			AmountMinor: fmt.Sprintf("%d", sku.PriceAmountMinor),
			Scale:       sku.PriceScale,
		},
		StockHint: sku.StockHint,
		Specs:     sku.Specs,
		Media:     sku.Media,
	}
}

// buildSKUSearchText constructs a searchable text representation from a SKU search result.
func buildSKUSearchText(sku model.SKUSearchResult) string {
	text := sku.Title + " " + sku.SkuID
	// Append specs as text if available.
	if len(sku.Specs) > 0 && string(sku.Specs) != "{}" {
		text += " " + string(sku.Specs)
	}
	return text
}

// ToLegacyResult converts HybridSearchResults to the legacy SearchResult format
// for backward compatibility with existing API consumers.
func ToLegacyResult(results []HybridSearchResult, total int, limit, offset int) *SearchResult {
	items := make([]model.SKUSearchResult, 0, len(results))
	for _, r := range results {
		items = append(items, r.SKU)
	}
	if total == 0 {
		total = len(items)
	}
	return &SearchResult{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}
}

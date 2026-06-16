package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ancf-commerce/ancf/services/catalog/internal/model"
)

// RAGService provides Retrieval-Augmented Generation for Agent product discovery.
// It combines hybrid search (keyword + vector semantic) with context building
// to enable natural-language product queries from Agents.
// 中文说明：面向 Agent 的检索增强(RAG)服务，融合混合检索(关键词+向量语义)与上下文构建，支持自然语言商品查询。
type RAGService struct {
	hybridSearch *HybridSearchService
}

// NewRAGService creates a new RAGService.
func NewRAGService(hybridSearch *HybridSearchService) *RAGService {
	return &RAGService{hybridSearch: hybridSearch}
}

// AgentSearchResponse is the structured response returned to Agents via the
// ancf:rag-search bridge command.
type AgentSearchResponse struct {
	Query     string               `json:"query"`
	Results   []HybridSearchResult `json:"results"`
	Context   string               `json:"context"`
	Embedding string               `json:"embedding"`
	Mode      string               `json:"mode"`
	TopK      int                  `json:"top_k"`
}

// SearchForAgent performs an Agent-oriented semantic product search.
//
// Input: a natural language query (e.g., "I need a GPU for training large language models").
// Output: ranked product results with a context summary usable by the Agent.
//
// The context field contains a formatted text block the Agent can use for reasoning.
func (s *RAGService) SearchForAgent(ctx context.Context, agentQuery string, topK int, mode SearchMode) (*AgentSearchResponse, error) {
	if topK < 1 || topK > 20 {
		topK = 5
	}

	results, err := s.hybridSearch.Search(ctx, agentQuery, topK, mode)
	if err != nil {
		return nil, fmt.Errorf("rag_service: search: %w", err)
	}

	embeddingStatus := "simulated"
	if s.hybridSearch.IsVectorAvailable() {
		embeddingStatus = "generated"
	}

	return &AgentSearchResponse{
		Query:     agentQuery,
		Results:   results,
		Context:   buildContext(results),
		Embedding: embeddingStatus,
		Mode:      string(mode),
		TopK:      topK,
	}, nil
}

// buildContext builds a human/Agent-readable context text from search results.
func buildContext(results []HybridSearchResult) string {
	if len(results) == 0 {
		return "No products found matching the query."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant products:\n", len(results)))

	for i, r := range results {
		sku := r.SKU
		priceStr := formatPrice(sku.Price.AmountMinor, sku.Price.Scale, sku.Price.Currency)
		specSummary := extractSpecSummary(sku.Specs)

		sb.WriteString(fmt.Sprintf("[%d] %s (%s) at %s",
			i+1, sku.Title, sku.SkuID, priceStr))
		if specSummary != "" {
			sb.WriteString(fmt.Sprintf(" -- %s", specSummary))
		}
		if r.Source != "fts" && r.Source != "" {
			sb.WriteString(fmt.Sprintf(" [source: %s]", r.Source))
		}
		if sku.StockHint > 0 {
			sb.WriteString(fmt.Sprintf(" (stock: %d)", sku.StockHint))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatPrice converts amount_minor (string), scale (int), and currency to a display string.
func formatPrice(amountMinor string, scale int, currency string) string {
	if amountMinor == "" || amountMinor == "0" {
		return fmt.Sprintf("0 %s/%s", currency, unitFromCurrency(currency))
	}

	intPart := ""
	fracPart := ""
	if len(amountMinor) > scale {
		intPart = amountMinor[:len(amountMinor)-scale]
		fracPart = amountMinor[len(amountMinor)-scale:]
	} else {
		intPart = "0"
		fracPart = fmt.Sprintf("%0*s", scale, amountMinor)
	}

	fracPart = strings.TrimRight(fracPart, "0")
	if fracPart == "" {
		return fmt.Sprintf("%s %s/%s", intPart, currency, unitFromCurrency(currency))
	}
	return fmt.Sprintf("%s.%s %s/%s", intPart, fracPart, currency, unitFromCurrency(currency))
}

// unitFromCurrency returns the pricing unit for common currencies.
func unitFromCurrency(currency string) string {
	switch currency {
	case "vUSDC", "USDC":
		return "hr"
	case "CNY":
		return "session"
	default:
		return "unit"
	}
}

// extractSpecSummary extracts a human-readable summary from SKU specs JSON.
func extractSpecSummary(specs []byte) string {
	if len(specs) == 0 {
		return ""
	}

	specsStr := string(specs)
	var parts []string

	if gpu := extractJSONValue(specsStr, "GPU"); gpu != "" {
		parts = append(parts, gpu+" GPU")
	}
	if mem := extractJSONValue(specsStr, "GPU_Memory"); mem != "" {
		parts = append(parts, mem)
	}
	if vcpu := extractJSONValue(specsStr, "vCPU"); vcpu != "" {
		parts = append(parts, vcpu+" vCPU")
	}
	if ram := extractJSONValue(specsStr, "RAM"); ram != "" {
		parts = append(parts, ram+" RAM")
	}

	return strings.Join(parts, ", ")
}

// extractJSONValue is a minimal JSON value extractor for flat objects.
func extractJSONValue(jsonStr, key string) string {
	searchKey := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(jsonStr, searchKey)
	if idx < 0 {
		return ""
	}

	rest := jsonStr[idx+len(searchKey):]
	rest = strings.TrimLeft(rest, " \t\n\r")

	if len(rest) == 0 {
		return ""
	}

	if rest[0] == '"' {
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return ""
		}
		return rest[1 : end+1]
	}

	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// SearchResultDisplay converts HybridSearchResults to legacy-format SKUSearchResults.
func SearchResultDisplay(results []HybridSearchResult) ([]model.SKUSearchResult, int) {
	items := make([]model.SKUSearchResult, 0, len(results))
	for _, r := range results {
		items = append(items, r.SKU)
	}
	return items, len(items)
}

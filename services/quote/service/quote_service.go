package service

import (
	catalogRepo "github.com/ancf-commerce/ancf/services/catalog/repository"
	internal "github.com/ancf-commerce/ancf/services/quote/internal/service"
	quoteRepo "github.com/ancf-commerce/ancf/services/quote/repository"
)

type QuoteService = internal.QuoteService

func NewQuoteService(repo *quoteRepo.QuoteRepository, skuRepo *catalogRepo.SKURepository) *QuoteService {
	return internal.NewQuoteService(repo, skuRepo)
}

package repository

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/quote/internal/repository"
)

type QuoteRepository = internal.QuoteRepository

func NewQuoteRepository(db *sql.DB) *QuoteRepository {
	return internal.NewQuoteRepository(db)
}

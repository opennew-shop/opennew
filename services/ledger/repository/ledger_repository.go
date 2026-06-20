package repository

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/ledger/internal/repository"
)

type LedgerRepository = internal.LedgerRepository

func NewLedgerRepository(db *sql.DB) *LedgerRepository {
	return internal.NewLedgerRepository(db)
}

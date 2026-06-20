package service

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/ledger/internal/service"
	ledgerRepo "github.com/ancf-commerce/ancf/services/ledger/repository"
)

type LedgerService = internal.LedgerService

func NewLedgerService(repo *ledgerRepo.LedgerRepository, db *sql.DB) *LedgerService {
	return internal.NewLedgerService(repo, db)
}

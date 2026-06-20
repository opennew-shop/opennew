package repository

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

type OutboxEvent = internal.OutboxEvent
type OutboxRepository = internal.OutboxRepository
type ChainRepository = internal.ChainRepository

func NewOutboxRepository(db *sql.DB) *OutboxRepository {
	return internal.NewOutboxRepository(db)
}

func NewChainRepository(db *sql.DB) *ChainRepository {
	return internal.NewChainRepository(db)
}

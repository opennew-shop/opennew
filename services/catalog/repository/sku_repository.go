package repository

import (
	"database/sql"

	internal "github.com/ancf-commerce/ancf/services/catalog/internal/repository"
)

type SKURepository = internal.SKURepository

func NewSKURepository(db *sql.DB) *SKURepository {
	return internal.NewSKURepository(db)
}

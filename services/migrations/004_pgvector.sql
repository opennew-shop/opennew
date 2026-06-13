-- ============================================================================
-- ANCF Commerce - pgvector + Embedding Infrastructure
-- Version: 004
-- Description: Enables pgvector extension, adds embedding column to
--              catalog_skus, and creates vector index for ANN search.
--              Compatible with OpenAI text-embedding-3-small (1536-dim).
-- ============================================================================

BEGIN;

-- Enable pgvector extension (requires superuser or CREATE EXTENSION privilege)
CREATE EXTENSION IF NOT EXISTS vector;

-- Add embedding column (1536-dim for text-embedding-3-small)
-- Using NULL as default since embeddings are generated asynchronously
ALTER TABLE catalog_skus ADD COLUMN IF NOT EXISTS embedding vector(1536);

-- Create IVFFlat index for approximate nearest neighbor (ANN) search.
-- Uses cosine distance operator (<=>) which is best for text embeddings.
-- lists = 100 is a reasonable default; tune to sqrt(row_count) for production.
-- Note: IVFFlat requires at least some data in the table before building.
-- If the table is empty, the index will still be created but may be less effective
-- until data is loaded and the index is rebuilt.
CREATE INDEX IF NOT EXISTS idx_catalog_skus_embedding
    ON catalog_skus USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Function to compute cosine similarity between two vectors
-- Returns a value between -1 and 1, where 1 means identical direction.
-- Usage: SELECT * FROM catalog_skus ORDER BY embedding <=> query_vector LIMIT 10;
-- The <=> operator computes cosine distance; 1 - distance = similarity.

COMMIT;

-- ============================================================================
-- Rollback (for reference, not executed automatically)
-- ============================================================================
-- BEGIN;
-- DROP INDEX IF EXISTS idx_catalog_skus_embedding;
-- ALTER TABLE catalog_skus DROP COLUMN IF EXISTS embedding;
-- DROP EXTENSION IF EXISTS vector;
-- COMMIT;

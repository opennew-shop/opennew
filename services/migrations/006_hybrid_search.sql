-- ============================================================================
-- ANCF Commerce - Hybrid Search Function (FTS + Vector)
-- Version: 006
-- Description: Creates a hybrid_search function combining PostgreSQL FTS
--              (ts_rank) with pgvector cosine similarity for semantic search.
--              Requires 004_pgvector.sql to have been applied first.
-- ============================================================================

BEGIN;

-- Hybrid search function (FTS + vector)
CREATE OR REPLACE FUNCTION hybrid_search(
    query_text TEXT, query_embedding vector(1536), top_k INT DEFAULT 10
) RETURNS TABLE(sku_id TEXT, title TEXT, score FLOAT) AS $$
BEGIN
    RETURN QUERY
    SELECT c.sku_id, c.title,
        COALESCE(ts_rank(c.search_vector, plainto_tsquery('english', query_text)), 0) * 0.3 +
        COALESCE(1 - (c.embedding <=> query_embedding), 0) * 0.7 AS score
    FROM catalog_skus c
    WHERE c.status = 'active'
    ORDER BY score DESC LIMIT top_k;
END;
$$ LANGUAGE plpgsql;

COMMIT;

-- ============================================================================
-- Rollback (for reference)
-- ============================================================================
-- BEGIN;
-- DROP FUNCTION IF EXISTS hybrid_search(TEXT, vector(1536), INT);
-- COMMIT;

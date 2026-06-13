#!/bin/bash
# 一键部署 pgvector + 运行迁移
docker compose -f infra/docker-compose-pgvector.yml up -d
echo "Waiting for PostgreSQL..."
sleep 5
docker compose -f infra/docker-compose-pgvector.yml exec -T postgres psql -U ancf -d ancf -f /docker-entrypoint-initdb.d/006_hybrid_search.sql
echo "pgvector ready. Run: go run services/catalog/cmd/main.go"

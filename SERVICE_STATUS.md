# ANCF Service Status

> Verifiable service inventory — auto-updated on each deployment
> Last verified: 2026-06-13
> Protocol version: ANCF-1.0

## Service Health Matrix

| # | Service | Port | Language | Status | Health Check | DB Tables | Key Dependencies |
|---|---------|------|----------|--------|-------------|-----------|-----------------|
| 1 | api-gateway | 8080 | Go 1.22 | Active | `GET /health` | - | Redis, Auth Middleware |
| 2 | catalog | 8081 | Go 1.22 | Active | `GET /health` | catalog_skus | PostgreSQL FTS |
| 3 | quote | 8081 | Go 1.22 | Active | `GET /health` | quotes | Redis TTL |
| 4 | checkout | 8081 | Go 1.22 | Active | `GET /health` | order_intents, idempotency_keys | EdDSA Verify |
| 5 | ledger | 8082 | Go 1.22 | Active | `GET /health` | ledger_entries | SERIALIZABLE isolation |
| 6 | mint | 8083 | Go 1.22 | Active | `GET /health` | mint_requests, redemption_requests, reserve_accounts | Outbox Pattern |
| 7 | chain-adapter | 8084 | Go 1.22 | Active | `GET /health` | chain_txs | Solana RPC |
| 8 | provisioning | 8085 | Go 1.22 | Active | `GET /health` | outbox | Redis Streams |
| 9 | audit | 8089 | Go 1.22 | Active | `GET /health` | audit_log | INSERT-only table |
| 10 | firmware | 8090 | Go 1.22 | Active | `GET /health` | - | SRI sha384 |
| 11 | payment | 8091 | Go 1.22 | Active | `GET /health` | - | Alipay A2A Rail |
| 12 | migrations | - | SQL | Active | Schema check | All 13 tables + 35 indexes | PostgreSQL 16 |

## API Endpoints — Full Inventory

| Method | Path | Service | Auth Required | Status | Verifiable |
|--------|------|---------|---------------|--------|------------|
| GET | `/.well-known/agent-rules.json` | api-gateway | No | Active | `curl -s http://127.0.0.1:8080/.well-known/agent-rules.json` |
| GET | `/health` | api-gateway | No | Active | `curl -s http://127.0.0.1:8080/health` |
| GET | `/api/v1/cli/search` | catalog | No | Active | `curl -s "http://127.0.0.1:8080/api/v1/cli/search?q=H100"` |
| POST | `/api/v1/cli/quote` | quote | No | Active | See test-sandbox.cjs Phase 3 |
| POST | `/api/v1/cli/checkout/prepare` | checkout | No | Active | See test-sandbox.cjs Phase 3 |
| POST | `/api/v1/cli/checkout/commit` | checkout | Idempotency-Key | Active | See test-sandbox.cjs Phase 3 |
| GET | `/api/v1/wallet/balance` | ledger | No | Active | Balance query endpoint |
| POST | `/api/v1/wallet/deposit-intents` | mint | No | Active | See test-sandbox.cjs Phase 4 |
| POST | `/api/v1/wallet/deposit-confirm` | mint | Internal | Active | See test-sandbox.cjs Phase 4 |
| POST | `/api/v1/wallet/redeem` | mint | No | Active | Redemption workflow |
| GET | `/api/v1/wallet/mint-status` | mint | No | Active | `?request_id=xxx` |
| GET | `/api/v1/wallet/redeem-status` | mint | No | Active | `?request_id=xxx` |
| GET | `/api/v1/wallet/reserve-info` | mint | No | Active | `?asset_symbol=AGP&network=solana-mainnet` |
| POST | `/api/v1/chain/simulate-deposit` | chain-adapter | Dev only | Active | Dev tool |
| GET | `/api/v1/chain/tx/:tx_hash` | chain-adapter | No | Active | Transaction lookup |
| GET | `/api/v1/admin/audit` | audit | Admin | Active | Immutable log query |
| POST | `/api/v1/admin/reconcile` | mint | Admin | Active | Reserve check |
| GET | `/api/v1/admin/provision-status/:id` | provisioning | Admin | Active | Outbox status |
| GET | `/api/v1/cli/provision-access/:id` | provisioning | No | Active | Access check |

## Agent Endpoints

| Method | Path | Auth Required | Status |
|--------|------|---------------|--------|
| POST | `/api/v1/auth/register-agent` | No | Active |
| POST | `/api/v1/auth/bind-wallet` | X-ANCF-Agent-Token | Active |
| GET | `/api/v1/auth/agent-info` | X-ANCF-Agent-Token | Active |
| POST | `/api/v1/catalog/products` | X-ANCF-Agent-Token | Active |
| GET | `/api/v1/catalog/products` | No | Active |
| GET | `/api/v1/catalog/products/:sku_id` | No | Active |
| PUT | `/api/v1/catalog/products/:sku_id` | X-ANCF-Agent-Token | Active |
| DELETE | `/api/v1/catalog/products/:sku_id` | X-ANCF-Agent-Token | Active |

## Escrow Endpoints (SUB-029)

| Method | Path | Description | Status |
|--------|------|-------------|--------|
| POST | `/api/v1/escrow/lock` | Lock buyer funds in custodial escrow | Active |
| POST | `/api/v1/escrow/confirm-delivery` | Seller confirms service delivery | Active |
| POST | `/api/v1/escrow/confirm-receipt` | Buyer confirms receipt | Active |
| POST | `/api/v1/escrow/release` | Trigger escrow auto-release | Active |
| GET | `/api/v1/escrow/status` | Query escrow by order_id | Active |
| GET | `/api/v1/escrow/history` | View escrow history for agent | Active |

## Dispute DAO Endpoints (SUB-030)

| Method | Path | Description | Status |
|--------|------|-------------|--------|
| POST | `/api/v1/dispute/file` | File dispute, freeze funds | Active |
| POST | `/api/v1/dispute/vote` | Sanction committee weighted vote | Active |
| GET | `/api/v1/dispute/status` | Query dispute by dispute_id | Active |
| GET | `/api/v1/dispute/list` | List disputes for agent | Active |
| POST | `/api/v1/dispute/join-committee` | Apply to join committee | Active |
| GET | `/api/v1/dispute/committee` | View committee members | Active |

## Multi-Chain Payment Rails (SUB-028)

| Rail Key | Token | Chain | Network | Status |
|----------|-------|-------|---------|--------|
| USDC-solana | USDC | solana | mainnet | Active |
| USDT-solana | USDT | solana | mainnet | Active |
| USDC-solana-devnet | USDC | solana | devnet | Active |
| AGP-solana-devnet | AGP | solana | devnet | Active |
| USDC-ethereum-sepolia | USDC | ethereum | sepolia | Active |
| USDC-ethereum | USDC | ethereum | mainnet | Active |
| USDT-trc20 | USDT | tron | mainnet | Active |
| USDC-trc20 | USDC | tron | mainnet | Active |
| USDT-trc20-shasta | USDT | tron | shasta | Active |
| USDT-bsc | USDT | bsc | mainnet | Active |
| USDC-bsc | USDC | bsc | mainnet | Active |
| BUSD-bsc | BUSD | bsc | mainnet | Active |
| USDT-bsc-testnet | USDT | bsc | testnet | Active |
| USDC-bsc-testnet | USDC | bsc | testnet | Active |
| USDT-polygon-mumbai | USDT | polygon | mumbai | Active |

## Payment Endpoints

| Method | Path | Description | Status |
|--------|------|-------------|--------|
| POST | `/api/v1/payments/create-link` | Create payment URL | Active |
| GET | `/api/v1/payments/status` | Query payment status | Active |

## Database Tables (13 + 35 indexes)

| # | Table | Schema | Write Policy | Status |
|---|-------|--------|-------------|--------|
| 1 | assets | mint | Immutable definition | Active |
| 2 | reserve_accounts | mint | Admin only | Active |
| 3 | mint_policies | mint | Admin only | Active |
| 4 | catalog_skus | catalog | Agent CRUD + GIN FTS index | Active |
| 5 | quotes | quote | Expire/consume markers | Active |
| 6 | order_intents | checkout | 8-state machine | Active |
| 7 | idempotency_keys | checkout | 24h TTL, unique constraint | Active |
| 8 | ledger_entries | ledger | INSERT-only, double-entry | Active |
| 9 | mint_requests | mint | 9-state | Active |
| 10 | redemption_requests | mint | 8-state | Active |
| 11 | chain_txs | chain-adapter | INSERT on confirm | Active |
| 12 | audit_log | audit | INSERT-only, immutable | Active |
| 13 | outbox | provisioning | INSERT, DELETE on publish | Active |

## JSON Schemas (5)

| # | Schema | File | Version |
|---|--------|------|---------|
| 1 | Manifest | `schemas/manifest.schema.json` | draft-2020-12 |
| 2 | Search Response | `schemas/search-response.schema.json` | draft-2020-12 |
| 3 | Quote | `schemas/quote.schema.json` | draft-2020-12 |
| 4 | Checkout | `schemas/checkout.schema.json` | draft-2020-12 |
| 5 | Mint/Redemption | `schemas/mint.schema.json` | draft-2020-12 |

## Security Measures

| # | Measure | Layer | Status |
|---|---------|-------|--------|
| 1 | API Key Authentication | Gateway middleware | Active |
| 2 | Token Bucket Rate Limiting | Gateway (Redis) | Active |
| 3 | JSON Schema Validation | Gateway middleware | Active |
| 4 | HTTP Signature (RFC 9421) | Gateway middleware | Active |
| 5 | EdDSA Wallet Signature | Checkout commit | Active |
| 6 | Idempotency Key (unique) | Checkout (24h TTL) | Active |
| 7 | Idempotency 3-way resolution | Checkout | Active |
| 8 | Quote Short TTL (5 min) | Quote service | Active |
| 9 | Quote Atomic Consumption | Quote (SELECT FOR UPDATE) | Active |
| 10 | Stock Concurrent Lock | Catalog (SELECT FOR UPDATE) | Active |
| 11 | SERIALIZABLE Isolation | All PostgreSQL transactions | Active |
| 12 | Double-Entry Ledger | Ledger (immutable) | Active |
| 13 | Advisory Lock (pg) | Ledger (anti-double-spend) | Active |
| 14 | Outbox Pattern | Provisioning | Active |
| 15 | CSP Headers (no eval) | Gateway | Active |
| 16 | Firmware SRI (sha384) | Firmware service | Active |
| 17 | Immutable Audit Log | Audit (INSERT-only) | Active |
| 18 | Reserve Reconciliation | Mint (reserve_constraint) | Active |
| 19 | Risk Check | Mint (daily/wallet/amount) | Active |
| 20 | Multisig Mint Authority (2-of-3) | Chain-adapter (Phase 4) | Active |
| 21 | Wallet Address Detection | Catalog (SUB-038) | Active |
| 22 | Media URL Allowlist | Catalog (SUB-038) | Active |
| 23 | Agent Token Ownership | Catalog (SUB-026) | Active |

## Seed Data

| SKU ID | Title | Price | Stock | Currency |
|--------|-------|-------|-------|----------|
| sku_gpu_h100_v1 | H100 Compute Rental, Hourly | 2.45/hr | 42 | AGP |
| sku_gpu_a100_v1 | A100 Compute Rental, Hourly | 1.20/hr | 128 | AGP |
| sku_gpu_l40s_v1 | L40S Compute Rental, Hourly | 0.65/hr | 256 | AGP |

## Reserve Accounts

| Network | Asset | Reserve Balance | Status |
|---------|-------|----------------|--------|
| solana-mainnet | AGP | 100,000 AGP | Active |
| sonic-l2 | AGP | 50,000 AGP | Active |

## Quick Verify

```bash
# 1. Health check
curl -s http://127.0.0.1:8080/health | jq .

# 2. Manifest
curl -s http://127.0.0.1:8080/.well-known/agent-rules.json | jq .protocol_version

# 3. Search
curl -s "http://127.0.0.1:8080/api/v1/cli/search?q=H100" | jq .total

# 4. Reserve check
curl -s "http://127.0.0.1:8080/api/v1/wallet/reserve-info?network=solana-mainnet&asset_symbol=AGP" | jq .

# 5. Reconciliation
curl -s "http://127.0.0.1:8080/api/v1/admin/reconciliation?asset_symbol=AGP" | jq .is_balanced

# 6. Full sandbox test
node test-sandbox.cjs

# 7. Security audit
node test-security-audit.cjs
```

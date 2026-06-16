package service

import "database/sql"

// recommendedIsolation returns the recommended transaction isolation level for
// the CommitCheckout flow.
//
// Design rationale:
//
// Current: SERIALIZABLE (the most strict level in PostgreSQL using SSI —
// Serializable Snapshot Isolation). Under high concurrency, SSI produces a
// significant number of serialization failures (SQLSTATE 40001), requiring
// application-level retry loops which reduce throughput.
//
// Recommended: READ COMMITTED + explicit row-level locks (SELECT FOR UPDATE).
//
//  1. CommitCheckout already acquires explicit row locks on every critical row:
//     - idempotency_keys (INSERT ... ON CONFLICT)
//     - quotes (SELECT FOR UPDATE via LockAndValidateQuoteTx)
//     - order_intents (SELECT FOR UPDATE via LockIntentForUpdate)
//     - catalog_skus (SELECT FOR UPDATE via LockSKUForUpdate)
//
//  2. With these explicit locks, all race conditions are already prevented at
//     the row level. SSI adds no additional safety but introduces unnecessary
//     serialization failures that degrade throughput.
//
//  3. READ COMMITTED + explicit row locks provides strictly higher concurrency
//     throughput with the same correctness guarantees.
//
// Risk assessment: LOW — all critical rows are locked explicitly. The only
// theoretical risk is a phantom read on a table without explicit locking, but
// the checkout flow locks every row it depends on.
//
// PostgreSQL default is READ COMMITTED, and this is the standard level used by
// most high-throughput OLTP applications.
//
// 中文说明：CommitCheckout 已对 idempotency_keys / quotes / order_intents / catalog_skus
// 等所有关键行显式加锁，因此采用 READ COMMITTED + 行级锁即可在保持同等正确性的前提下，
// 获得比 SERIALIZABLE(SSI) 更高的并发吞吐，并避免 40001 序列化失败重试。
func recommendedIsolation() sql.IsolationLevel {
	return sql.LevelReadCommitted
}

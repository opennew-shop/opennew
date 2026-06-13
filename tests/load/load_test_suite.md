# ANCF Load Test Suite

## Overview

This document describes the load testing strategy for ANCF Zero-Frontend Commerce. The load test suite uses both Go benchmarks (`testing.B`) and external HTTP load testing tools (`vegeta` or `k6`) to validate system performance under expected production loads.

## Performance Targets

| Endpoint | Target Throughput | P99 Latency | Max Latency |
|----------|-------------------|-------------|-------------|
| `GET /api/v1/cli/search` | 100 req/s | < 50ms | < 100ms |
| `POST /api/v1/cli/quote` | 50 req/s | < 100ms | < 200ms |
| `POST /api/v1/cli/checkout/commit` | 20 req/s | < 500ms | < 1000ms |
| `GET /api/v1/wallet/balance` | 200 req/s | < 20ms | < 50ms |
| `POST /api/v1/wallet/deposit-intents` | 10 req/s | < 200ms | < 500ms |
| `POST /api/v1/wallet/redeem` | 5 req/s | < 500ms | < 1000ms |

## Running Go Benchmarks

```bash
# Run all load benchmarks
go test -bench=. -benchmem -benchtime=3s ./tests/load/

# Run specific benchmark
go test -bench=BenchmarkSearchAPI -benchmem ./tests/load/
go test -bench=BenchmarkQuoteAPI -benchmem ./tests/load/
go test -bench=BenchmarkCheckoutCommit -benchmem ./tests/load/
go test -bench=BenchmarkLedgerBalance -benchmem ./tests/load/

# Run benchmarks with CPU profiling
go test -bench=. -cpuprofile=cpu.prof ./tests/load/
go tool pprof cpu.prof

# Run load scenario tests (T tests, not benchmarks)
go test -run TestLoad -v ./tests/load/
```

## HTTP Load Testing with Vegeta

### Prerequisites

```bash
# Install vegeta
go install github.com/tsenart/vegeta@latest
```

### Search API Load Test

```bash
# Generate 100 req/s for 30 seconds
echo "GET http://127.0.0.1:8080/api/v1/cli/search?q=H100" | \
  vegeta attack -rate=100 -duration=30s | \
  vegeta report

# With multiple query patterns
cat > targets.txt <<'EOF'
GET http://127.0.0.1:8080/api/v1/cli/search?q=H100
GET http://127.0.0.1:8080/api/v1/cli/search?q=A100
GET http://127.0.0.1:8080/api/v1/cli/search?q=GPU
GET http://127.0.0.1:8080/api/v1/cli/search?q=Compute
EOF

vegeta attack -targets=targets.txt -rate=100 -duration=30s | vegeta report
```

### Quote API Load Test

```bash
# Create quote request body file
cat > quote_body.json <<'EOF'
{"lines":[{"sku_id":"sku_gpu_h100_v1","quantity":2}]}
EOF

# Target: 50 req/s
echo "POST http://127.0.0.1:8080/api/v1/cli/quote" | \
  vegeta attack -rate=50 -duration=30s -body=quote_body.json \
  -header="Content-Type: application/json" \
  -header="X-API-Key: test-api-key" | \
  vegeta report
```

### Checkout Commit Load Test

```bash
# Requires quote_id and order_intent_id from prepare step
# Target: 20 req/s (with idempotency keys)

# Step 1: Prepare quote + intent
# Step 2: Sign payload with wallet
# Step 3: Run load test against commit endpoint

cat > commit_targets.txt <<'EOF'
POST http://127.0.0.1:8080/api/v1/cli/checkout/commit
Content-Type: application/json
Idempotency-Key: {{.idem_key}}
X-API-Key: test-api-key
@commit_body.json
EOF

vegeta attack -targets=commit_targets.txt -rate=20 -duration=30s | vegeta report
```

### Steady-State Soak Test

```bash
# Sustained load: 60s at target throughput
vegeta attack -targets=search_targets.txt -rate=100 -duration=60s | \
  tee results.bin | vegeta report

# Check for latency degradation over time
cat results.bin | vegeta plot > plot.html
```

## HTTP Load Testing with k6

### k6 Test Script

```javascript
// load_test_ancf.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const searchErrorRate = new Rate('search_errors');
const searchLatency = new Trend('search_latency');
const quoteErrorRate = new Rate('quote_errors');
const quoteLatency = new Trend('quote_latency');
const commitErrorRate = new Rate('commit_errors');
const commitLatency = new Trend('commit_latency');

const BASE_URL = 'http://127.0.0.1:8080';

export const options = {
  scenarios: {
    search_load: {
      executor: 'constant-arrival-rate',
      rate: 100,
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 50,
      exec: 'searchTest',
    },
    quote_load: {
      executor: 'constant-arrival-rate',
      rate: 50,
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 25,
      startTime: '2s',
      exec: 'quoteTest',
    },
    commit_load: {
      executor: 'constant-arrival-rate',
      rate: 20,
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 10,
      startTime: '4s',
      exec: 'commitTest',
    },
  },
  thresholds: {
    'search_latency': ['p(99)<50'],
    'quote_latency': ['p(99)<100'],
    'commit_latency': ['p(99)<500'],
    'search_errors': ['rate<0.01'],
    'quote_errors': ['rate<0.01'],
    'commit_errors': ['rate<0.05'],
  },
};

export function searchTest() {
  const queries = ['H100', 'A100', 'L40S', 'GPU', 'Compute'];
  const q = queries[Math.floor(Math.random() * queries.length)];

  const start = Date.now();
  const res = http.get(`${BASE_URL}/api/v1/cli/search?q=${q}`);
  searchLatency.add(Date.now() - start);

  const ok = check(res, {
    'search status 200': (r) => r.status === 200,
    'search has items': (r) => JSON.parse(r.body).items.length > 0,
  });
  searchErrorRate.add(!ok);

  sleep(0.1);
}

export function quoteTest() {
  const payload = JSON.stringify({
    lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 1 }],
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': 'test-api-key',
    },
  };

  const start = Date.now();
  const res = http.post(`${BASE_URL}/api/v1/cli/quote`, payload, params);
  quoteLatency.add(Date.now() - start);

  const ok = check(res, {
    'quote status 200': (r) => r.status === 200,
    'quote has quote_id': (r) => JSON.parse(r.body).quote_id !== '',
  });
  quoteErrorRate.add(!ok);

  sleep(0.2);
}

export function commitTest() {
  // Note: requires pre-generated quote_id and signed payload.
  // This is a skeleton showing the expected structure.
  const idemKey = `k6_test_${__VU}_${__ITER}`;
  const payload = JSON.stringify({
    order_intent_id: 'intent_k6_test',
    quote_id: 'quote_k6_test',
    wallet: 'test_wallet_address',
    wallet_signature: 'test_signature_base64',
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Idempotency-Key': idemKey,
      'X-API-Key': 'test-api-key',
    },
  };

  const start = Date.now();
  const res = http.post(`${BASE_URL}/api/v1/cli/checkout/commit`, payload, params);
  commitLatency.add(Date.now() - start);

  const ok = check(res, {
    'commit status in [200, 409]': (r) => r.status === 200 || r.status === 409,
  });
  commitErrorRate.add(!ok && res.status !== 409); // 409 is expected for replayed keys.

  sleep(0.5);
}
```

### Running k6

```bash
# Install k6
# macOS: brew install k6
# Linux: snap install k6
# Windows: choco install k6

# Run the test
k6 run load_test_ancf.js

# Run with output to CSV for analysis
k6 run --out csv=results.csv load_test_ancf.js

# Run with specific scenarios only
k6 run --env SCENARIO=search load_test_ancf.js
```

## Load Test Scenarios

### 1. Baseline Search Test

- **Duration**: 60 seconds
- **Rate**: 100 req/s sustained
- **Queries**: Random selection from [H100, A100, L40S, GPU, Compute]
- **Success Criteria**: P99 < 50ms, Error rate < 1%

### 2. Quote Burst Test

- **Duration**: 30 seconds
- **Rate**: 50 req/s burst
- **Body**: Single and multi-line quote requests
- **Success Criteria**: P99 < 100ms, Error rate < 1%

### 3. Checkout Commit Endurance Test

- **Duration**: 120 seconds
- **Rate**: 20 req/s sustained
- **Idempotency**: Unique keys per request
- **Success Criteria**: P99 < 500ms, No idempotency conflicts (all unique keys)

### 4. Mixed Workload Test

- **Duration**: 60 seconds
- **Concurrent scenarios**:
  - Search: 80 req/s
  - Quote: 30 req/s
  - Checkout: 10 req/s
- **Success Criteria**: All P99 within targets, No resource exhaustion

### 5. Idempotency Stress Test

- **Duration**: 30 seconds
- **Rate**: 20 req/s with 50% duplicated keys (different bodies)
- **Expected**: ~50% 409 Conflict responses, ~50% 200 OK

### 6. Inventory Exhaustion Test

- **Duration**: Runs until stock depletes
- **Rate**: 30 req/s against same SKU with limited stock
- **Expected**: First N succeed, remaining get 409 Conflict (out of stock)

## Monitoring During Load Tests

### Key Metrics to Watch

1. **Application**:
   - Request latency (P50, P95, P99)
   - Request throughput (req/s)
   - Error rate (4xx, 5xx)
   - Idempotency replay rate

2. **PostgreSQL**:
   - Active connections
   - Row lock wait time (inventory, ledger)
   - Transaction rate
   - Deadlock count

3. **Redis**:
   - Cache hit rate
   - Rate limit token consumption
   - Connection count

4. **System**:
   - CPU utilization
   - Memory usage
   - Network I/O
   - Go garbage collection pauses

### PostgreSQL Monitoring Queries

```sql
-- Active locks
SELECT locktype, relation::regclass, mode, granted
FROM pg_locks WHERE NOT granted;

-- Long-running transactions
SELECT pid, now() - xact_start AS duration, query
FROM pg_stat_activity
WHERE state = 'active' AND now() - xact_start > interval '1 second';

-- Connection count
SELECT count(*) FROM pg_stat_activity;
```

## Failure Injection Tests

### 1. Redis Unavailable

```bash
# Stop Redis during load test
docker stop ancf-redis

# Verify: Rate limiting falls back to local in-memory
# Idempotency: Falls back to database-only check
# Expected: Graceful degradation, no 5xx spikes
```

### 2. PostgreSQL Connection Pool Exhaustion

```bash
# Reduce max_connections temporarily
# Expected: Connection timeout errors, retry with backoff
# Verify: No panic, clean error responses
```

### 3. High Latency Simulation

```bash
# Add artificial latency to database queries
# Verify: P99 increases but system remains stable
# Expected: No cascading failures
```

## Reporting

Load test results should be reported in the following format:

```json
{
  "test_name": "search_api_baseline",
  "timestamp": "2026-06-08T00:00:00Z",
  "duration_seconds": 60,
  "target_rate": 100,
  "actual_rate": 98.5,
  "total_requests": 5910,
  "latency_ms": {
    "min": 1.2,
    "mean": 12.4,
    "p50": 10.1,
    "p95": 28.3,
    "p99": 42.7,
    "max": 156.2
  },
  "status_codes": {
    "200": 5880,
    "400": 15,
    "429": 15,
    "500": 0
  },
  "error_rate": 0.005,
  "passed": true
}
```

## Continuous Load Testing

Integrate load tests into CI/CD:

```yaml
# GitHub Actions workflow snippet
load-test:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - name: Start services
      run: docker-compose up -d postgres redis api-gateway
    - name: Wait for healthy
      run: ./scripts/wait-for-healthy.sh
    - name: Run load tests
      run: |
        go test -bench=BenchmarkSearchAPI -benchtime=5s ./tests/load/
        vegeta attack -targets=targets.txt -rate=100 -duration=30s | vegeta report
    - name: Stop services
      run: docker-compose down
```

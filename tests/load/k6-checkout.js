// k6 load test: ANCF checkout flow
// Usage: k6 run tests/load/k6-checkout.js
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '30s', target: 10 },  // ramp-up
    { duration: '1m',  target: 50 },  // steady load
    { duration: '30s', target: 0 },   // ramp-down
  ],
  thresholds: {
    http_req_duration: ['p(95)<2000'],
    http_req_failed: ['rate<0.05'],
  },
};

const BASE = 'http://127.0.0.1:8080';

export default function () {
  // Step 1: Search
  const searchRes = http.get(`${BASE}/api/v1/cli/search?q=H100&limit=1`);
  check(searchRes, { 'search ok': (r) => r.status === 200 });

  // Step 2: Quote
  const quoteRes = http.post(`${BASE}/api/v1/cli/quote`, JSON.stringify({
    wallet: 'k6_test_wallet', network: 'solana-mainnet',
    lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 1 }]
  }), { headers: { 'Content-Type': 'application/json' } });
  check(quoteRes, { 'quote ok': (r) => r.status === 200 });

  sleep(1);
}

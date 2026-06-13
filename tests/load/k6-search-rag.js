// RAG search load test
import http from 'k6/http';
import { check } from 'k6';

export const options = { vus: 20, duration: '1m' };

const BASE = 'http://127.0.0.1:8080';
const queries = [
  'fast GPU for AI', 'cheapest compute', 'H100 rental',
  'A100 specs', 'L40S price', 'GPU with 80GB memory',
  'CUDA 12.4', 'render farm', 'machine learning card', 'inference GPU'
];

export default function () {
  const q = queries[Math.floor(Math.random() * queries.length)];
  const res = http.get(`${BASE}/api/v1/cli/rag-search?q=${encodeURIComponent(q)}&top_k=3`);
  check(res, { 'rag ok': (r) => r.status === 200 });
}

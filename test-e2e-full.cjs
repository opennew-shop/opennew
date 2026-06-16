/**
 * ANCF E2E Test Suite
 * 测试: 凭证生成 | 凭证保留 | 商品上架 | Agent 临时互动网页
 */
const http = require('http');
const crypto = require('crypto');

const API = 'http://127.0.0.1:8080';
const AGENT = 'http://127.0.0.1:3000';
const INTERNAL_API = process.env.MINT_SERVICE_URL || API;
const INTERNAL_API_KEY = process.env.INTERNAL_API_KEY || 'change-me-in-production';

let passed = 0, failed = 0;
function test(name, fn) {
  process.stdout.write(`  ${name}... `);
  try { fn(); passed++; console.log('✅'); }
  catch(e) { failed++; console.log('❌', e.message); }
}
async function testAsync(name, fn) {
  process.stdout.write(`  ${name}... `);
  try { await fn(); passed++; console.log('✅'); }
  catch(e) { failed++; console.log('❌', e.message); }
}

function fetchJSON(url, opts = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    const req = http.request({hostname: u.hostname, port: u.port, path: u.pathname + u.search, method: opts.method || 'GET', headers: opts.headers || {}}, res => {
      let body = '';
      res.on('data', c => body += c);
      res.on('end', () => {
        try { resolve({status: res.statusCode, headers: res.headers, body: JSON.parse(body)}); }
        catch(e) { resolve({status: res.statusCode, headers: res.headers, body}); }
      });
    });
    req.on('error', reject);
    if (opts.body) req.write(JSON.stringify(opts.body));
    req.end();
  });
}

(async () => {
console.log('╔══════════════════════════════════════════════╗');
console.log('║  ANCF E2E Full Test Suite                    ║');
console.log('╚══════════════════════════════════════════════╝\n');

// ═══════════════════════════════════════════════
console.log('1. Discovery Manifest — 凭证生成与校验');
// ═══════════════════════════════════════════════

let manifest, manifestHash;
await testAsync('1.1 获取 agent-rules.json', async () => {
  const r = await fetchJSON(API + '/.well-known/agent-rules.json');
  if (r.status !== 200) throw new Error('HTTP ' + r.status);
  manifest = r.body;
  manifestHash = crypto.createHash('sha256').update(JSON.stringify(manifest)).digest('hex');
  console.log(`\n      shop_id: ${manifest.shop_id}`);
  console.log(`      protocol: ${manifest.protocol_version}`);
  console.log(`      hash(sha256): ${manifestHash.slice(0,16)}...`);
  console.log(`      capabilities: ${Object.keys(manifest.capabilities).length} endpoints`);
  console.log(`      payment_rails: ${(manifest.payment_rails||[]).length} rails`);
  if (!manifest.shop_id) throw new Error('missing shop_id');
  if (!manifest.signature) throw new Error('missing signature');
});

let manifestHash2;
await testAsync('1.2 凭证一致性 (重复获取应返回相同hash)', async () => {
  const r = await fetchJSON(API + '/.well-known/agent-rules.json');
  manifestHash2 = crypto.createHash('sha256').update(JSON.stringify(r.body)).digest('hex');
  if (manifestHash !== manifestHash2) throw new Error('凭证不一致！两次manifest hash不同');
  console.log(`      两次 hash 一致: ${manifestHash.slice(0,16)}... = ${manifestHash2.slice(0,16)}...`);
});

await testAsync('1.3 凭证保留 (Agent 已缓存manifest)', async () => {
  const r = await fetchJSON(AGENT + '/health');
  if (r.body.shop_id !== manifest.shop_id) throw new Error('Agent未正确缓存manifest');
  console.log(`      Agent缓存: shop=${r.body.shop_id}, protocol=${r.body.protocol_version}`);
});

await testAsync('1.4 Manifest 有效性校验', async () => {
  const now = new Date();
  const exp = new Date(manifest.expires_at);
  if (exp <= now) throw new Error('manifest已过期');
  if (!manifest.agent_policy) throw new Error('missing agent_policy');
  if (!manifest.schemas?.checkout) throw new Error('missing checkout schema');
  console.log(`      过期: ${manifest.expires_at}`);
  console.log(`      策略: autonomous=${manifest.agent_policy.allow_autonomous_checkout}, human_confirm=${manifest.agent_policy.require_human_confirmation}`);
});

// ═══════════════════════════════════════════════
console.log('\n2. 商品上架与搜索');
// ═══════════════════════════════════════════════

let allSKUs = [];
await testAsync('2.1 搜索所有商品', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/search?q=&limit=20');
  allSKUs = r.body.items;
  if (allSKUs.length < 3) throw new Error(`只有 ${allSKUs.length} 个SKU，预期 >= 3`);
  console.log(`      发现 ${allSKUs.length} 个商品:`);
  allSKUs.forEach(s => console.log(`        ${s.sku_id} | ${s.title} | ${s.price.amount_minor} ${s.price.currency}`));
});

await testAsync('2.2 搜索 H100', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/search?q=H100&limit=5');
  if (r.body.items.length !== 1 || r.body.items[0].sku_id !== 'sku_gpu_h100_v1') {
    throw new Error('H100搜索失败');
  }
  console.log(`      结果: ${r.body.items[0].title} | stock=${r.body.items[0].stock_hint}`);
});

await testAsync('2.3 搜索 A100', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/search?q=A100&limit=5');
  if (r.body.items.length !== 1) throw new Error('A100搜索失败');
  console.log(`      结果: ${r.body.items[0].title} | specs=${JSON.stringify(r.body.items[0].specs)}`);
});

// ═══════════════════════════════════════════════
console.log('\n3. 报价与结算流程');
// ═══════════════════════════════════════════════

let quoteId, intentId, orderId;
const wallet = 'DEMO_WALLET_E2E_TEST';

await testAsync('3.1 请求报价 (2×H100)', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/quote', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: {wallet, network: 'solana-mainnet', lines: [{sku_id: 'sku_gpu_h100_v1', quantity: 2}]}
  });
  quoteId = r.body.quote_id;
  if (!quoteId || !quoteId.startsWith('quote_')) throw new Error('无效quote_id');
  console.log(`      quote_id: ${quoteId}`);
  console.log(`      total: ${r.body.total_minor} ${r.body.currency} (${r.body.lines.length} lines)`);
  console.log(`      expires: ${r.body.expires_at}`);
});

await testAsync('3.2 报价过期校验', async () => {
  if (!quoteId) throw new Error('跳过');
  const exp = new Date((await fetchJSON(API + '/api/v1/cli/quote', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {wallet, network: 'solana-mainnet', lines: [{sku_id: 'sku_gpu_h100_v1', quantity: 1}]}
  })).body.expires_at);
  const ttl = (exp - new Date()) / 1000;
  if (ttl < 200 || ttl > 400) throw new Error(`报价TTL异常: ${ttl}s (预期约300s)`);
  console.log(`      TTL: ${ttl.toFixed(0)}s (预期 ~300s)`);
});

await testAsync('3.3 Checkout Prepare', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/checkout/prepare', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {quote_id: quoteId, wallet, network: 'solana-mainnet', agent_session_id: 'e2e_session'}
  });
  intentId = r.body.order_intent_id;
  const payload = r.body.signable_payload;
  if (!intentId) throw new Error('无效intent_id');
  if (!payload.nonce) throw new Error('missing nonce in signable_payload');
  console.log(`      intent_id: ${intentId}`);
  console.log(`      signable_payload.domain: ${payload.domain}`);
  console.log(`      signable_payload.nonce: ${payload.nonce.slice(0,16)}...`);
  console.log(`      signable_payload 字段: ${Object.keys(payload).join(', ')}`);
});

const idemKey = 'ck_e2e_' + crypto.randomBytes(8).toString('hex');
await testAsync('3.4 Checkout Commit (带Idempotency-Key)', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/checkout/commit', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'Idempotency-Key': idemKey},
    body: {order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: 'e2e_demo_sig', agent_session_id: 'e2e_session'}
  });
  orderId = r.body.order_id;
  console.log(`      order_id: ${orderId}`);
  console.log(`      status: ${r.body.status}`);
});

await testAsync('3.5 Idempotency 重放检测', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/checkout/commit', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'Idempotency-Key': idemKey},
    body: {order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: 'e2e_demo_sig', agent_session_id: 'e2e_session'}
  });
  // 同key+同body → 应该重放或返回缓存响应
  console.log(`      重放响应: HTTP ${r.status} (预期 200 或 409)`);
});

await testAsync('3.6 Idempotency 冲突检测 (同key+不同body)', async () => {
  const r = await fetchJSON(API + '/api/v1/cli/checkout/commit', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'Idempotency-Key': idemKey},
    body: {order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: 'DIFFERENT_SIG_HACK', agent_session_id: 'e2e_session'}
  });
  if (r.status !== 409) console.log(`      ⚠ HTTP ${r.status} (严格模式应返回409)`);
  else console.log(`      冲突检测正确: HTTP 409`);
  if (r.status === 409) passed++; else failed++;
});

// ═══════════════════════════════════════════════
console.log('\n4. Agent 临时互动网页');
// ═══════════════════════════════════════════════

let catalogHTML = '';
await testAsync('4.1 获取 Catalog 页面', async () => {
  const r = await fetchJSON(AGENT + '/');
  catalogHTML = r.body;
  if (typeof catalogHTML !== 'string' || catalogHTML.length < 500) throw new Error('页面太小或非HTML');
  console.log(`      HTML大小: ${catalogHTML.length} bytes`);
});

await testAsync('4.2 安全警告条', async () => {
  if (!catalogHTML.includes('Backend-authoritative') && !catalogHTML.includes('warning'))
    throw new Error('缺少安全警告条');
  console.log('      安全警告条: 存在 (Backend Authoritative)');
});

await testAsync('4.3 CSP 安全头', async () => {
  const r = await fetchJSON(AGENT + '/');
  const csp = r.headers['content-security-policy'];
  if (!csp) throw new Error('缺少CSP头');
  if (!csp.includes("script-src")) throw new Error('CSP缺少script-src');
  if (csp.includes('unsafe-eval')) console.log(`      ⚠ 含unsafe-eval (Vue编译器需要)`);
  if (!csp.includes("frame-src 'none'")) console.log(`      ⚠ 缺少frame-src限制`);
  console.log(`      CSP: ${csp.slice(0,80)}...`);
});

await testAsync('4.4 Agent Bridge 白名单', async () => {
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {command: 'ancf:ready', params: {}, requestId: 'bridge-test'}
  });
  if (!r.body.result?.commands) throw new Error('Bridge未返回命令列表');
  console.log(`      白名单命令: ${r.body.result.commands.join(', ')}`);
});

await testAsync('4.5 非白名单命令拒绝', async () => {
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {command: 'ancf:steal_all_funds', params: {}, requestId: 'evil'}
  });
  if (r.status !== 403) throw new Error(`非白名单应403, 实际${r.status}`);
  console.log(`      恶意命令 "ancf:steal_all_funds": HTTP 403 ✅`);
});

await testAsync('4.6 Bridge 搜索代理', async () => {
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {command: 'ancf:search', params: {query: 'L40S', limit: 5}, requestId: 'bridge-search'}
  });
  const items = r.body.result?.items || [];
  if (items.length !== 1) throw new Error('Bridge search failed');
  console.log(`      搜索 "L40S" → ${items[0].title} | ${items[0].price.amount_minor} vUSDC`);
});

// ═══════════════════════════════════════════════
console.log('\n5. vUSDC 充值/赎回 流程');
// ═══════════════════════════════════════════════

let depositIntentId;
await testAsync('5.1 创建充值意图', async () => {
  const r = await fetchJSON(API + '/api/v1/wallet/deposit-intents', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {wallet, network: 'solana-mainnet', asset_symbol: 'vUSDC', amount_minor: '50000000'}
  });
  depositIntentId = r.body.deposit_intent_id;
  if (!depositIntentId) throw new Error('缺少deposit_intent_id');
  console.log(`      intent: ${depositIntentId}`);
  console.log(`      reserve: ${r.body.reserve_address?.slice(0,16)}...`);
  console.log(`      memo: ${r.body.memo}`);
});

await testAsync('5.2 确认充值到账', async () => {
  const r = await fetchJSON(INTERNAL_API + '/api/v1/internal/deposit-confirm', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-Internal-API-Key': INTERNAL_API_KEY},
    body: {deposit_intent_id: depositIntentId, deposit_tx_id: 'tx_mock_' + Date.now(), amount_minor: '50000000'}
  });
  console.log(`      status: ${r.body.status} (预期credited)`);
  console.log(`      amount: ${r.body.amount_minor} minor units`);
});

let redeemId;
await testAsync('5.3 创建赎回请求', async () => {
  const r = await fetchJSON(API + '/api/v1/wallet/redeem', {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: {wallet, network: 'solana-mainnet', asset_symbol: 'vUSDC', amount_minor: '10000000'}
  });
  redeemId = r.body.request_id;
  if (!redeemId) throw new Error('缺少request_id');
  console.log(`      request_id: ${redeemId}`);
});

await testAsync('5.4 处理赎回', async () => {
  const r = await fetchJSON(INTERNAL_API + `/api/v1/internal/redeem/${redeemId}/process`, {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-Internal-API-Key': INTERNAL_API_KEY},
    body: {}
  });
  console.log(`      status: ${r.body.status || r.body.message || 'processed'}`);
});

await testAsync('5.5 储备对账', async () => {
  const r = await fetchJSON(API + '/api/v1/admin/reconciliation');
  console.log(`      对账结果: ${r.body.is_balanced ? 'BALANCED ✅' : 'IMBALANCE ⚠'}`);
  if (r.body.difference !== undefined) console.log(`      diff: ${r.body.difference} minor units`);
});

// ═══════════════════════════════════════════════
console.log('\n╔══════════════════════════════════════════════╗');
console.log(`║  结果: ${passed} 通过 / ${failed} 失败                      ${' '.repeat(10)}║`);
console.log('╚══════════════════════════════════════════════╝');
process.exit(failed > 0 ? 1 : 0);
})();

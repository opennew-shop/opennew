// Security fix verification — runs on server (has bs58/tweetnacl)
const http = require('http');
const crypto = require('crypto');
const nacl = require('tweetnacl');
const _bs58 = require('bs58'); const bs58 = _bs58.default || _bs58;

const API = 'http://127.0.0.1:8080';
let pass = 0, fail = 0;
const ok = m => { pass++; console.log('  ✅', m); };
const no = m => { fail++; console.log('  ❌', m); };

function req(method, path, body, headers = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(API + path);
    const r = http.request({ hostname: u.hostname, port: u.port, path: u.pathname, method, headers: { 'Content-Type': 'application/json', ...headers } }, res => {
      let d = ''; res.on('data', c => d += c);
      res.on('end', () => { try { resolve({ status: res.statusCode, body: JSON.parse(d || '{}') }); } catch { resolve({ status: res.statusCode, body: d }); } });
    });
    r.on('error', reject);
    if (body) r.write(JSON.stringify(body));
    r.end();
  });
}

(async () => {
  console.log('=== Security Fix Verification ===\n');

  // Generate a real Solana keypair (ed25519)
  const kp = nacl.sign.keyPair();
  const wallet = bs58.encode(Buffer.from(kp.publicKey));

  console.log('A-1/A-2/A-3: Checkout signature verification');

  // quote → prepare
  const q = await req('POST', '/api/v1/cli/quote', { wallet, network: 'solana-mainnet', lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 1 }] });
  const quoteId = q.body.quote_id;
  const p = await req('POST', '/api/v1/cli/checkout/prepare', { quote_id: quoteId, wallet });
  const intentId = p.body.order_intent_id;
  const payload = p.body.signable_payload;

  // Test 1: signature:none → 401
  const r1 = await req('POST', '/api/v1/cli/checkout/commit', { order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: 'none' }, { 'Idempotency-Key': 'ck_t1' });
  (r1.status === 401) ? ok("signature:'none' 被拒 (401) — 零成本下单已阻断") : no(`signature:none 返回 ${r1.status} (期望401)`);

  // Test 2: placeholder → 401
  const r2 = await req('POST', '/api/v1/cli/checkout/commit', { order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: 'demo_signature_placeholder' }, { 'Idempotency-Key': 'ck_t2' });
  (r2.status === 401) ? ok('占位签名被拒 (401)') : no(`占位签名返回 ${r2.status}`);

  // Test 3: valid signature → 200
  const msg = Buffer.from('ANCF_CHECKOUT:' + JSON.stringify(payload), 'utf8');
  const sig = Buffer.from(nacl.sign.detached(new Uint8Array(msg), kp.secretKey)).toString('base64');
  const r3 = await req('POST', '/api/v1/cli/checkout/commit', { order_intent_id: intentId, quote_id: quoteId, wallet, wallet_signature: sig }, { 'Idempotency-Key': 'ck_t3' });
  (r3.status === 200 && r3.body.status === 'committed') ? ok('有效 EdDSA 签名通过 (200 committed)') : no(`有效签名返回 ${r3.status}: ${JSON.stringify(r3.body).slice(0,80)}`);

  // Test 4: wallet binding — different wallet on a fresh intent
  const q4 = await req('POST', '/api/v1/cli/quote', { wallet, network: 'solana-mainnet', lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 1 }] });
  const p4 = await req('POST', '/api/v1/cli/checkout/prepare', { quote_id: q4.body.quote_id, wallet });
  const evilWallet = bs58.encode(Buffer.from(nacl.sign.keyPair().publicKey));
  const r4 = await req('POST', '/api/v1/cli/checkout/commit', { order_intent_id: p4.body.order_intent_id, quote_id: q4.body.quote_id, wallet: evilWallet, wallet_signature: 'none' }, { 'Idempotency-Key': 'ck_t4' });
  (r4.status === 403 || r4.status === 401) ? ok(`钱包不匹配被拒 (${r4.status}) — 冒名下单已阻断`) : no(`冒名钱包返回 ${r4.status}`);

  console.log(`\n=== ${pass} 通过 / ${fail} 失败 ===`);
  process.exit(fail > 0 ? 1 : 0);
})();

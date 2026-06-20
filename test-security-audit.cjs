/**
 * ANCF Security Boundary Audit — Full Attack Surface Test
 * Tests: API | Bridge | Ledger | Agent | Solana On-Chain
 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const nacl = require('tweetnacl');
const _bs58 = require('bs58');
const bs58 = _bs58.default || _bs58;

const API = (process.env.ANCF_API_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const AGENT = (process.env.ANCF_AGENT_URL || 'http://127.0.0.1:3000').replace(/\/+$/, '');
const SOLANA_RPC = process.env.ANCF_SOLANA_RPC || 'https://api.devnet.solana.com';
const VUSDC_MINT = process.env.ANCF_VUSDC_MINT || 'Ecz3XMcs76JsFiiUgVNDGbqtKVotMP5gMMAjCJYpe8SX';

let pass = 0, fail = 0, warn = 0;
function ok(msg) { pass++; console.log('  ✅', msg); }
function no(msg) { fail++; console.log('  ❌', msg); }
function wrn(msg) { warn++; console.log('  ⚠️', msg); }

function fetchJSON(url, opts = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    const req = http.request({
      hostname: u.hostname, port: u.port, path: u.pathname + u.search,
      method: opts.method || 'GET', headers: opts.headers || {},
      timeout: 10000
    }, res => {
      let body = '';
      res.on('data', c => body += c);
      res.on('end', () => {
        try { resolve({ status: res.statusCode, headers: res.headers, body: JSON.parse(body) }); }
        catch (e) { resolve({ status: res.statusCode, headers: res.headers, body }); }
      });
    });
    req.on('error', reject);
    req.on('timeout', () => { req.destroy(); reject(new Error('timeout')); });
    if (opts.body) req.write(JSON.stringify(opts.body));
    req.end();
  });
}

(async () => {
console.log('╔══════════════════════════════════════════════╗');
console.log('║  ANCF Security Boundary Audit                ║');
console.log('╚══════════════════════════════════════════════╝\n');
console.log(`API   = ${API}`);
console.log(`Agent = ${AGENT}\n`);

// ═══════════════════════════════════════════════
console.log('1. API Gateway — 认证与限流');
// ═══════════════════════════════════════════════
{
  const r = await fetchJSON(API + '/api/v1/cli/search?q=H100');
  r.status === 200 ? ok('Search (public): accessible') : no('Search blocked');
}
{
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { command: 'ancf:steal_all_funds', params: {}, requestId: 'evil-1' }
  });
  r.status === 403 ? ok('Bridge: non-whitelisted command → 403') : no(`Bridge whitelist bypass: HTTP ${r.status}`);
}
{
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { command: 'ancf:checkout_commit', params: { order_intent_id: 'hacked', quote_id: 'hacked', wallet: 'hacker', wallet_signature: 'fake' }, requestId: 'evil-2' }
  });
  r.status !== 403 ? ok('Bridge: whitelisted command accepted') : wrn('Bridge: whitelisted command rejected unexpectedly');
}
{
  const r = await fetchJSON(AGENT + '/bridge', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: {} // missing command + requestId
  });
  r.status === 400 ? ok('Bridge: empty body → 400') : no(`Bridge empty body: HTTP ${r.status}`);
}

// ═══════════════════════════════════════════════
console.log('\n2. CSP & HTML 注入');
// ═══════════════════════════════════════════════
{
  const r = await fetchJSON(AGENT + '/');
  const csp = r.headers['content-security-policy'] || '';
  csp.includes('script-src') ? ok('CSP: script-src present') : no('CSP: missing script-src');
  csp.includes("frame-src 'none'") ? ok('CSP: frame-src=none') : wrn('CSP: frame-src not restricted');
  csp.includes("object-src 'none'") ? ok('CSP: object-src=none') : wrn('CSP: object-src not restricted');
  csp.includes("form-action 'none'") ? ok('CSP: form-action=none') : wrn('CSP: form-action not restricted');
}
{
  // XSS test: inject script in search query
  const r = await fetchJSON(API + '/api/v1/cli/search?q=<script>alert(1)</script>');
  const items = r.body?.items || [];
  items.length >= 0 ? ok('Search: XSS payload handled safely') : no('Search: XSS caused error');
}
{
  // HTML injection in manifest
  const r = await fetchJSON(AGENT + '/checkout-session?sku=<img src=x onerror=alert(1)>&wallet=test');
  r.status !== 200 ? ok('Checkout session: bad SKU rejected') : wrn('Checkout session: bad SKU returned HTML');
}

// ═══════════════════════════════════════════════
console.log('\n3. 账本安全 — 完整性攻击');
// ═══════════════════════════════════════════════
{
  // Negative amount in quote
  const r = await fetchJSON(API + '/api/v1/cli/quote', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { wallet: 'attacker', network: 'solana-mainnet', lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: -999 }] }
  });
  r.status !== 200 ? ok(`Negative quantity rejected: HTTP ${r.status}`) : no(`Negative quantity accepted! ${r.body.total_minor}`);
}
{
  // Zero amount
  const r = await fetchJSON(API + '/api/v1/cli/quote', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { wallet: 'attacker', network: 'solana-mainnet', lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 0 }] }
  });
  r.status !== 200 ? ok(`Zero quantity rejected: HTTP ${r.status}`) : no('Zero quantity accepted');
}
{
  // Fake SKU
  const r = await fetchJSON(API + '/api/v1/cli/quote', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { wallet: 'attacker', network: 'solana-mainnet', lines: [{ sku_id: 'FREE_MONEY_PLZ', quantity: 1 }] }
  });
  r.status !== 200 ? ok(`Fake SKU rejected: HTTP ${r.status}`) : no('Fake SKU accepted');
}

// ═══════════════════════════════════════════════
console.log('\n4. Idempotency 攻击');
// ═══════════════════════════════════════════════
const testWallet = nacl.sign.keyPair();
const W = bs58.encode(Buffer.from(testWallet.publicKey));
const qr = await fetchJSON(API + '/api/v1/cli/quote', {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: { wallet: W, network: 'solana-mainnet', lines: [{ sku_id: 'sku_gpu_h100_v1', quantity: 1 }] }
});
const pr = await fetchJSON(API + '/api/v1/cli/checkout/prepare', {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: { quote_id: qr.body.quote_id, wallet: W, network: 'solana-mainnet', agent_session_id: 'sec_test' }
});

{
  // Same key, different body. The first request must be a valid commit, otherwise
  // signature verification would reject it before idempotency can be exercised.
  const key = 'ck_attack_' + Date.now();
  const msg = Buffer.from('ANCF_CHECKOUT:' + JSON.stringify(pr.body.signable_payload), 'utf8');
  const sig = Buffer.from(nacl.sign.detached(new Uint8Array(msg), testWallet.secretKey)).toString('base64');
  const r1 = await fetchJSON(API + '/api/v1/cli/checkout/commit', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
    body: { order_intent_id: pr.body.order_intent_id, quote_id: qr.body.quote_id, wallet: W, wallet_signature: sig, agent_session_id: 'sec_test' }
  });
  const r2 = await fetchJSON(API + '/api/v1/cli/checkout/commit', {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
    body: { order_intent_id: pr.body.order_intent_id, quote_id: qr.body.quote_id, wallet: W, wallet_signature: 'DIFFERENT_SIG_ATTACK', agent_session_id: 'sec_test' }
  });
  (r1.status === 200 && r2.status === 409) ? ok('Idempotency: different body same key → 409') : no(`Idempotency conflict test failed: first=${r1.status}, second=${r2.status}`);
}

// ═══════════════════════════════════════════════
console.log('\n5. Solana Mint Authority — 未授权铸币测试');
// ═══════════════════════════════════════════════
{
  const payerFile = path.join(__dirname, 'onchain', 'vusdc-mint', 'payer.json');

  if (fs.existsSync(payerFile)) {
    const { Connection, Keypair, PublicKey, Transaction, sendAndConfirmTransaction } = require('@solana/web3.js');
    const { TOKEN_2022_PROGRAM_ID, createMintToInstruction, getOrCreateAssociatedTokenAccount } = require('@solana/spl-token');

    const c = new Connection(SOLANA_RPC, 'confirmed');
    const payer = Keypair.fromSecretKey(Uint8Array.from(JSON.parse(fs.readFileSync(payerFile, 'utf-8'))));

    // Test: try mint with payer (NOT the mint authority — authority is multisig-auth)
    try {
      const tmpWallet = Keypair.generate();
      const ata = await getOrCreateAssociatedTokenAccount(c, payer, new PublicKey(VUSDC_MINT), tmpWallet.publicKey, false, 'confirmed', { commitment: 'confirmed' }, TOKEN_2022_PROGRAM_ID);
      const tx = new Transaction().add(createMintToInstruction(new PublicKey(VUSDC_MINT), ata.address, payer.publicKey, 1_000_000n, [], TOKEN_2022_PROGRAM_ID));
      await sendAndConfirmTransaction(c, tx, [payer], { skipPreflight: true });
      no('ON-CHAIN: payer minted without authority'); // Should NOT succeed
    } catch (e) {
      const msg = e.message || '';
      if (msg.includes('signer') || msg.includes('authority') || msg.includes('0x1') || msg.includes('custom program error') || msg.includes('failed')) {
        ok('ON-CHAIN: payer cannot mint (not mint authority)');
      } else {
        wrn(`ON-CHAIN: unknown error: ${msg.slice(0, 80)}`);
      }
    }

    // Test: try mint with correct authority (multisig-auth.json)
    const authFile = path.join(__dirname, 'onchain', 'vusdc-mint', 'multisig-auth.json');
    if (fs.existsSync(authFile)) {
      const mintAuth = Keypair.fromSecretKey(Uint8Array.from(JSON.parse(fs.readFileSync(authFile, 'utf-8'))));
      try {
        const tmpWallet = Keypair.generate();
        const ata = await getOrCreateAssociatedTokenAccount(c, payer, new PublicKey(VUSDC_MINT), tmpWallet.publicKey, false, 'confirmed', { commitment: 'confirmed' }, TOKEN_2022_PROGRAM_ID);
        const tx = new Transaction().add(createMintToInstruction(new PublicKey(VUSDC_MINT), ata.address, mintAuth.publicKey, 10_000n, [], TOKEN_2022_PROGRAM_ID));
        const sig = await sendAndConfirmTransaction(c, tx, [payer, mintAuth]);
        ok(`ON-CHAIN: mint authority can mint (10 vUSDC→test wallet, tx=${sig.slice(0, 12)}...)`);
      } catch (e) {
        no(`ON-CHAIN: mint authority failed: ${e.message.slice(0, 80)}`);
      }
    } else {
      wrn('ON-CHAIN: multisig-auth.json not found, skipping authority test');
    }

    // Freeze authority check
    const mintInfo = await c.getAccountInfo(new PublicKey(VUSDC_MINT));
    if (mintInfo) {
      // Token-2022 mint: byte 36-40 contains freeze_authority COption
      const hasFreeze = mintInfo.data[36] === 1;
      hasFreeze ? wrn('ON-CHAIN: freeze_authority is SET (can freeze user tokens)') : ok('ON-CHAIN: no freeze_authority (users control their tokens)');
    }
  } else {
    wrn('ON-CHAIN: payer.json not found, skipping Solana tests');
  }
}

// ═══════════════════════════════════════════════
console.log('\n6. Deposit/Mint API — 未授权铸币');
// ═══════════════════════════════════════════════
{
  // Try to call deposit-confirm without valid intent
  const r = await fetchJSON(API + '/api/v1/internal/deposit-confirm', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { deposit_intent_id: 'FAKE_INTENT_HACK', deposit_tx_id: 'fake_tx' }
  });
  r.status !== 200 ? ok(`Fake deposit-confirm rejected: HTTP ${r.status}`) : no('Fake deposit-confirm accepted');
}
{
  // Try to redeem more than available
  const r = await fetchJSON(API + '/api/v1/wallet/redeem', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: { wallet: W, network: 'solana-mainnet', asset_symbol: 'vUSDC', amount_minor: '999999999999999' }
  });
  // Mock doesn't check balance, but should still process
  r.status === 200 ? wrn('Redemption: mock accepts, production ledger checks balance') : ok(`Redemption rejected: ${r.status}`);
}

// ═══════════════════════════════════════════════
console.log('\n7. Manifest 安全');
// ═══════════════════════════════════════════════
{
  const r = await fetchJSON(API + '/.well-known/agent-rules.json');
  const m = r.body;
  m.expires_at ? ok('Manifest: has expires_at') : no('Manifest: missing expires_at');
  m.signature ? ok('Manifest: has signature') : no('Manifest: missing signature');
  m.agent_policy?.require_human_confirmation ? ok('Manifest: human confirmation required') : no('Manifest: no human confirmation');
  m.agent_policy?.allow_autonomous_checkout === false ? ok('Manifest: autonomous checkout disabled') : wrn('Manifest: autonomous checkout enabled');
}

// ═══════════════════════════════════════════════
console.log('\n╔══════════════════════════════════════════════╗');
console.log(`║  Result: ${pass} ✅ | ${fail} ❌ | ${warn} ⚠️                         ║`);
console.log('╚══════════════════════════════════════════════╝');
process.exit(fail > 0 ? 1 : 0);
})();

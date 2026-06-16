/**
 * ANCF AgentPay Sandbox Test Environment (SUB-027)
 *
 * Standalone sandbox simulating the complete AgentPay (AGP) transaction flow:
 *   1. Agent registration → obtain token
 *   2. Agent uploads product → ownership binding
 *   3. Buyer search → quote → checkout
 *   4. AgentPay ledger verification (deposit, mint, balance check)
 *   5. Transaction record query
 *
 * Usage: node test-sandbox.cjs
 */

const http = require('http');

const BASE = 'http://127.0.0.1:8080';
const INTERNAL_BASE = process.env.MINT_SERVICE_URL || BASE;
const INTERNAL_API_KEY = process.env.INTERNAL_API_KEY || 'change-me-in-production';
const TIMEOUT = 10000;

// ---- HTTP Helpers ----
function httpRequest(method, path, body, headers) {
  return new Promise((resolve, reject) => {
    const url = new URL(path, BASE);
    const options = {
      method,
      hostname: url.hostname,
      port: url.port,
      path: url.pathname + url.search,
      headers: Object.assign({ 'Content-Type': 'application/json' }, headers || {}),
      timeout: TIMEOUT
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try {
          const json = JSON.parse(data);
          resolve({ status: res.statusCode, headers: res.headers, body: json });
        } catch (e) {
          resolve({ status: res.statusCode, headers: res.headers, body: data });
        }
      });
    });

    req.on('error', reject);
    req.on('timeout', () => { req.destroy(); reject(new Error('Request timeout')); });

    if (body) req.write(JSON.stringify(body));
    req.end();
  });
}

function get(path, headers) { return httpRequest('GET', path, null, headers); }
function post(path, body, headers) { return httpRequest('POST', path, body, headers); }
function internalPost(path, body) {
  return httpRequest('POST', new URL(path, INTERNAL_BASE).toString(), body, { 'X-Internal-API-Key': INTERNAL_API_KEY });
}

function generateId() {
  const hex = Array.from({ length: 32 }, () => Math.floor(Math.random() * 16).toString(16)).join('');
  return hex;
}

// ---- Test Runner ----
let passed = 0;
let failed = 0;

function assert(condition, message) {
  if (condition) {
    passed++;
    console.log(`  [PASS] ${message}`);
  } else {
    failed++;
    console.error(`  [FAIL] ${message}`);
  }
}

function section(title) {
  console.log(`\n${'='.repeat(60)}`);
  console.log(`  ${title}`);
  console.log(`${'='.repeat(60)}`);
}

// ---- Sandbox State ----
const state = {
  agentToken: null,
  agentId: null,
  agentName: 'SandboxSeller_' + generateId().slice(0, 8),
  skuId: null,
  quoteId: null,
  intentId: null,
  orderId: null,
  depositIntentId: null,
  mintRequestId: null,
  buyerWallet: 'BUYER_WALLET_SANDBOX_' + generateId().slice(0, 16),
};

// ---- Sandbox Flow ----
async function main() {
  console.log('');
  console.log('  ╔══════════════════════════════════════════════════════╗');
  console.log('  ║   ANCF AgentPay (AGP) Sandbox Test Environment      ║');
  console.log('  ║   SUB-027: AGP (AgentPay) Rename + Sandbox            ║');
  console.log('  ╚══════════════════════════════════════════════════════╝');

  let startTime = Date.now();

  // ================================================================
  // Phase 1: Agent Registration
  // ================================================================
  section('Phase 1: Agent Registration');

  try {
    // 1a. Register agent
    const regRes = await post('/api/v1/auth/register-agent', {
      agent_name: state.agentName,
      agent_type: 'seller'
    });
    assert(regRes.status === 201, `Agent registration returns 201 (got ${regRes.status})`);
    assert(!!regRes.body.token, 'Agent token is present');
    assert(!!regRes.body.agent_id, 'Agent ID is present');
    assert(regRes.body.agent_name === state.agentName, `Agent name matches: ${regRes.body.agent_name}`);

    state.agentToken = regRes.body.token;
    state.agentId = regRes.body.agent_id;
    console.log(`  [INFO] Agent registered: ${state.agentId} (${state.agentName})`);

    // 1b. Bind wallet to agent
    const bindRes = await post('/api/v1/auth/bind-wallet', {
      wallet_address: 'SELLER_WALLET_SANDBOX_ABC123',
      chain: 'solana',
      label: 'default'
    }, { 'X-ANCF-Agent-Token': state.agentToken });
    assert(bindRes.status === 200, `Wallet bind returns 200 (got ${bindRes.status})`);

    // 1c. Verify agent info
    const infoRes = await get('/api/v1/auth/agent-info', { 'X-ANCF-Agent-Token': state.agentToken });
    assert(infoRes.status === 200, `Agent info returns 200`);
    assert(infoRes.body.wallets.length > 0, 'Agent has bound wallets');
    console.log(`  [INFO] Wallet bound to agent: solana / SELLER_WALLET_SANDBOX_ABC123`);

  } catch (e) {
    console.error('  [ERROR] Agent registration failed:', e.message);
    failed++;
  }

  // ================================================================
  // Phase 2: Agent Uploads Product (Ownership Binding)
  // ================================================================
  section('Phase 2: Agent Uploads Product');

  try {
    const productRes = await post('/api/v1/catalog/products', {
      title: 'Sandbox GPU A100 Test SKU',
      description: 'A test product uploaded by sandbox agent',
      amount_minor: '990000',
      scale: 6,
      stock: 10,
      currency: 'AGP',
      specs: { GPU: 'A100 40GB', Memory: '40GB HBM2e' }
    }, { 'X-ANCF-Agent-Token': state.agentToken });

    assert(productRes.status === 201, `Product upload returns 201 (got ${productRes.status})`);
    assert(!!productRes.body.sku_id, 'SKU ID is present');
    assert(productRes.body.currency === 'AGP', `Currency is AGP: ${productRes.body.currency}`);
    assert(productRes.body.agent_id === state.agentId, `Ownership bound to agent: ${productRes.body.agent_id}`);

    state.skuId = productRes.body.sku_id;
    console.log(`  [INFO] Product uploaded: ${state.skuId}, price=0.99 AGP/hr, owner=${state.agentId}`);

    // Verify product ownership
    const infoRes2 = await get('/api/v1/auth/agent-info', { 'X-ANCF-Agent-Token': state.agentToken });
    assert(infoRes2.body.products_count > 0, `Product count > 0: ${infoRes2.body.products_count}`);
    assert(infoRes2.body.products.includes(state.skuId), 'Product appears in agent product list');

  } catch (e) {
    console.error('  [ERROR] Product upload failed:', e.message);
    failed++;
  }

  // ================================================================
  // Phase 3: Buyer Search → Quote → Checkout
  // ================================================================
  section('Phase 3: Buyer Search → Quote → Checkout');

  try {
    // 3a. Search for products
    const searchRes = await get('/api/v1/cli/search?q=Sandbox+GPU&limit=10');
    assert(searchRes.status === 200, `Search returns 200 (got ${searchRes.status})`);
    assert(searchRes.body.items.length > 0, 'Search returns products');

    const found = searchRes.body.items.find(i => i.price && i.price.currency === 'AGP');
    assert(!!found, `Found product with AGP currency`);
    console.log(`  [INFO] Search found ${searchRes.body.total} products, currency=AGP verified`);

    // 3b. Request quote
    const quoteRes = await post('/api/v1/cli/quote', {
      wallet: state.buyerWallet,
      network: 'solana-mainnet',
      lines: [{ sku_id: state.skuId, quantity: 2 }]
    });
    assert(quoteRes.status === 200, `Quote returns 200 (got ${quoteRes.status})`);
    assert(quoteRes.body.currency === 'AGP', `Quote currency is AGP: ${quoteRes.body.currency}`);
    assert(!!quoteRes.body.quote_id, 'Quote ID is present');
    assert(quoteRes.body.lines.length === 1, `Quote has 1 line (got ${quoteRes.body.lines.length})`);

    state.quoteId = quoteRes.body.quote_id;
    console.log(`  [INFO] Quote created: ${state.quoteId}, total=${quoteRes.body.total_minor} AGP minor`);

    // 3c. Prepare checkout
    const prepareRes = await post('/api/v1/cli/checkout/prepare', {
      quote_id: state.quoteId,
      wallet: state.buyerWallet,
      network: 'solana-devnet'
    });
    assert(prepareRes.status === 200, `Checkout prepare returns 200 (got ${prepareRes.status})`);
    assert(prepareRes.body.signable_payload.currency === 'AGP', `Payload currency is AGP: ${prepareRes.body.signable_payload.currency}`);
    assert(!!prepareRes.body.order_intent_id, 'Intent ID is present');

    state.intentId = prepareRes.body.order_intent_id;
    console.log(`  [INFO] Checkout prepared: intent=${state.intentId}`);

    // 3d. Commit checkout with Idempotency-Key
    const idempotencyKey = 'idem_sandbox_' + generateId();
    const commitRes = await post('/api/v1/cli/checkout/commit', {
      order_intent_id: state.intentId,
      quote_id: state.quoteId,
      wallet: state.buyerWallet,
      wallet_signature: 'sandbox_mock_sig_' + generateId().slice(0, 44),
      agent_session_id: 'session_sandbox_' + generateId().slice(0, 8)
    }, { 'Idempotency-Key': idempotencyKey });

    assert(commitRes.status === 200, `Checkout commit returns 200 (got ${commitRes.status})`);
    assert(!!commitRes.body.order_id, 'Order ID is present');
    assert(commitRes.body.transaction_recorded === true, 'Transaction recorded flag is true');

    state.orderId = commitRes.body.order_id;
    console.log(`  [INFO] Checkout committed: order=${state.orderId}, transaction_recorded=true`);

  } catch (e) {
    console.error('  [ERROR] Search/Quote/Checkout flow failed:', e.message);
    failed++;
  }

  // ================================================================
  // Phase 4: AgentPay Ledger Verification
  // ================================================================
  section('Phase 4: AgentPay (AGP) Ledger Verification');

  try {
    // 4a. Create deposit intent
    const depRes = await post('/api/v1/wallet/deposit-intents', {
      wallet: state.buyerWallet,
      network: 'solana-mainnet',
      asset_symbol: 'AGP',
      amount_minor: '50000000'
    });
    assert(depRes.status === 201, `Deposit intent returns 201 (got ${depRes.status})`);
    assert(!!depRes.body.deposit_intent_id, 'Deposit intent ID is present');
    assert(!!depRes.body.reserve_address, 'Reserve address is present');

    state.depositIntentId = depRes.body.deposit_intent_id;
    console.log(`  [INFO] Deposit intent created: ${state.depositIntentId}`);

    // 4b. Confirm deposit
    const confirmRes = await internalPost('/api/v1/internal/deposit-confirm', {
      deposit_intent_id: state.depositIntentId,
      deposit_tx_id: 'sandbox_tx_' + generateId().slice(0, 16),
      amount_minor: '50000000'
    });
    assert(confirmRes.status === 200, `Deposit confirm returns 200 (got ${confirmRes.status})`);
    assert(confirmRes.body.status === 'credited', `Deposit status is credited: ${confirmRes.body.status}`);

    state.mintRequestId = confirmRes.body.request_id;
    console.log(`  [INFO] Deposit confirmed and credited: ${state.mintRequestId}, 50 AGP`);

    // 4c. Check mint status
    const mintStatusRes = await get(`/api/v1/wallet/mint-status?request_id=${state.mintRequestId}`);
    assert(mintStatusRes.status === 200, `Mint status returns 200`);
    assert(mintStatusRes.body.asset_symbol === 'AGP', `Asset symbol is AGP: ${mintStatusRes.body.asset_symbol}`);

    // 4d. Check reserve info
    const reserveRes = await get('/api/v1/wallet/reserve-info?network=solana-mainnet&asset_symbol=AGP');
    assert(reserveRes.status === 200, `Reserve info returns 200`);
    assert(reserveRes.body.asset_symbol === 'AGP', `Reserve asset is AGP: ${reserveRes.body.asset_symbol}`);
    console.log(`  [INFO] Reserve info: solana-mainnet AGP reserve OK`);

    // 4e. Reconciliation check
    const reconRes = await get('/api/v1/admin/reconciliation?asset_symbol=AGP&network=solana-mainnet');
    assert(reconRes.status === 200, `Reconciliation returns 200 (got ${reconRes.status})`);
    assert(reconRes.body.asset_symbol === 'AGP', `Reconciliation asset is AGP: ${reconRes.body.asset_symbol}`);
    console.log(`  [INFO] Reconciliation: balanced=${reconRes.body.is_balanced}, diff=${reconRes.body.difference_minor}`);

  } catch (e) {
    console.error('  [ERROR] AgentPay ledger verification failed:', e.message);
    failed++;
  }

  // ================================================================
  // Phase 5: Transaction Record Query
  // ================================================================
  section('Phase 5: AgentPay Transaction Review');

  try {
    console.log(`  [INFO] Order ID: ${state.orderId}`);
    console.log(`  [INFO] Buyer Wallet: ${state.buyerWallet}`);
    console.log(`  [INFO] Seller Agent: ${state.agentId} (${state.agentName})`);
    console.log(`  [INFO] Payment Network: solana-devnet`);
    console.log(`  [INFO] Payment Token: AGP (AgentPay)`);
    console.log(`  [INFO] Quote ID: ${state.quoteId}`);
    console.log(`  [INFO] Intent ID: ${state.intentId}`);
    console.log(`  [INFO] Deposit Intent: ${state.depositIntentId}`);
    console.log(`  [INFO] Mint Request: ${state.mintRequestId}`);

    // Verify manifest shows AGP
    const manifestRes = await get('/.well-known/agent-rules.json');
    assert(manifestRes.status === 200, `Manifest returns 200`);
    const agpAsset = manifestRes.body.supported_assets.find(a => a.symbol === 'AGP');
    assert(!!agpAsset, 'Manifest lists AGP as supported asset');
    assert(agpAsset.decimals === 6, `AGP decimals = 6`);
    assert(agpAsset.type === 'shadow-ledger', `AGP type is shadow-ledger`);

    const agpRail = manifestRes.body.payment_rails.find(r => r.rail === 'agp_ledger');
    assert(!!agpRail, 'Manifest lists agentpay_ledger payment rail');
    assert(agpRail.currency === 'AGP', `Payment rail currency is AGP: ${agpRail.currency}`);
    console.log(`  [INFO] Manifest confirms: AGP supported, agentpay_ledger payment rail active`);

  } catch (e) {
    console.error('  [ERROR] Transaction review failed:', e.message);
    failed++;
  }

  // ================================================================
  // Summary
  // ================================================================
  const elapsed = ((Date.now() - startTime) / 1000).toFixed(2);
  section('Sandbox Results');
  console.log(`  Total: ${passed + failed} tests`);
  console.log(`  Passed: ${passed}`);
  console.log(`  Failed: ${failed}`);
  console.log(`  Duration: ${elapsed}s`);
  console.log(`  Payment Token: AGP (AgentPay)`);
  console.log(`  Currency: AGP`);
  console.log(`  Payment Rail: agentpay_ledger`);
  console.log('');

  if (failed > 0) {
    console.log('  Some tests FAILED. Check the output above for details.');
    process.exit(1);
  } else {
    console.log('  All tests PASSED. AgentPay (AGP) sandbox verification complete.');
    console.log('  AGP (AgentPay) rename verified across all endpoints.');
    process.exit(0);
  }
}

// Check if mock server is running first
async function checkServer() {
  try {
    const res = await get('/health');
    if (res.status === 200) {
      console.log('[Sandbox] Mock server is running. Starting tests...');
      return true;
    }
  } catch (e) {
    // server not running
  }
  return false;
}

checkServer().then(running => {
  if (!running) {
    console.log('[Sandbox] Mock server not running on port 8080.');
    console.log('[Sandbox] Start it with: node test-mock-server.cjs');
    console.log('[Sandbox] Then re-run: node test-sandbox.cjs');
    process.exit(1);
  }
  main().catch(err => {
    console.error('[Sandbox] Fatal error:', err.message);
    process.exit(1);
  });
});

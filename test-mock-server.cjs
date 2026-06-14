/**
 * ANCF Mock API Server for Agent Frontend Testing
 *
 * Simulates the Go API Gateway on port 8080:
 *   GET  /.well-known/agent-rules.json
 *   GET  /health
 *   GET  /api/v1/cli/search
 *   POST /api/v1/cli/quote
 *   POST /api/v1/cli/checkout/prepare
 *   POST /api/v1/cli/checkout/commit
 *
 * Usage: node test-mock-server.cjs
 */

const http = require('http');
const crypto = require('crypto');

const fs = require('fs');
const path = require('path');
const PORT = parseInt(process.env.PORT || '8080', 10);
const STATE_FILE = path.join(__dirname, '.mock-state.json');

// ---- File Persistence ----
let persistedState = {};
try { persistedState = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8')); console.log('[Persistence] Loaded state from .mock-state.json'); }
catch(e) { console.log('[Persistence] Fresh start (no saved state)'); }

function saveState(state) {
  try { fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2)); }
  catch(e) { console.error('[Persistence] Save failed:', e.message); }
}

// Save on every mutation — no timer waste
function persistNow() { saveState(getStateFn()); }
process.on('SIGINT', () => { persistNow(); process.exit(0); });
process.on('SIGTERM', () => { persistNow(); process.exit(0); });

// ---- Multi-Chain Payment Rails (SUB-028) ----
const PAYMENT_RAILS = {
  // ═══ Solana Mainnet ═══
  'USDC-solana': {
    token: 'USDC', chain: 'solana', network: 'mainnet',
    mint: 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v', decimals: 6,
    payment_url_template: 'solana:{wallet}?amount={amount}&spl-token={mint}&label={label}'
  },
  'USDT-solana': {
    token: 'USDT', chain: 'solana', network: 'mainnet',
    mint: 'Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB', decimals: 6,
    payment_url_template: 'solana:{wallet}?amount={amount}&spl-token={mint}&label={label}'
  },
  // ═══ Solana Devnet (测试沙箱) ═══
  // Faucet: solana airdrop 2 <wallet> --url devnet
  //         然后 swap SOL→devUSDC via https://spl-token-faucet.com
  'USDC-solana-devnet': {
    token: 'USDC', chain: 'solana', network: 'devnet',
    mint: '4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU', decimals: 6,
    payment_url_template: 'solana:{wallet}?amount={amount}&spl-token={mint}&label={label}&cluster=devnet',
    faucet: 'solana airdrop 2 <wallet> --url devnet; swap on jupiter devnet for USDC'
  },
  'AGP-solana-devnet': {
    token: 'AGP', chain: 'solana', network: 'devnet',
    mint: 'Ecz3XMcs76JsFiiUgVNDGbqtKVotMP5gMMAjCJYpe8SX', decimals: 6,
    payment_url_template: 'solana:{wallet}?amount={amount}&spl-token={mint}&label={label}&cluster=devnet'
  },
  // ═══ Ethereum Sepolia (测试沙箱) ═══
  // Faucet: https://sepoliafaucet.com | https://faucet.circle.com (USDC)
  'USDC-ethereum-sepolia': {
    token: 'USDC', chain: 'ethereum', network: 'sepolia',
    contract: '0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238', decimals: 6,
    payment_url_template: 'ethereum:sepolia:{wallet}?contractAddress={contract}',
    faucet: 'https://faucet.circle.com — 选择 Ethereum Sepolia, 输入地址获取测试 USDC'
  },
  // ═══ Ethereum Mainnet ═══
  'USDC-ethereum': {
    token: 'USDC', chain: 'ethereum', network: 'mainnet',
    contract: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48', decimals: 6,
    payment_url_template: 'ethereum:{wallet}?value=0&contractAddress={contract}&data=0x...'
  },
  // ═══ TRON Mainnet ═══
  'USDT-trc20': {
    token: 'USDT', chain: 'tron', network: 'mainnet',
    contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', decimals: 6,
    payment_url_template: 'tron:{wallet}?contract={contract}&amount={amount}'
  },
  'USDC-trc20': {
    token: 'USDC', chain: 'tron', network: 'mainnet',
    contract: 'TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8', decimals: 6,
    payment_url_template: 'tron:{wallet}?contract={contract}&amount={amount}&decimals=6'
  },
  // ═══ TRON Shasta (测试沙箱) ═══
  // Faucet: https://www.trongrid.io/shasta (每日1000 TRX + 测试USDT)
  // USDT contract on Shasta: TG3XXyExBkPp9nzdajDZsozEu4BkaSJozs
  'USDT-trc20-shasta': {
    token: 'USDT', chain: 'tron', network: 'shasta',
    contract: 'TG3XXyExBkPp9nzdajDZsozEu4BkaSJozs', decimals: 6,
    payment_url_template: 'tron:shasta:{wallet}?contract={contract}&amount={amount}&decimals=6',
    faucet: 'https://www.trongrid.io/shasta — TRX faucet + 测试 USDT 合约 TG3XXyExBkPp9nzdajDZsozEu4BkaSJozs'
  },
  // ═══ BSC Mainnet ═══
  'USDT-bsc': {
    token: 'USDT', chain: 'bsc', network: 'mainnet',
    contract: '0x55d398326f99059fF775485246999027B3197955', decimals: 18,
    payment_url_template: 'ethereum:{wallet}?value=0&contractAddress={contract}&data=0xa9059cbb'
  },
  'USDC-bsc': {
    token: 'USDC', chain: 'bsc', network: 'mainnet',
    contract: '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d', decimals: 18,
    payment_url_template: 'ethereum:{wallet}?value=0&contractAddress={contract}&data=0xa9059cbb'
  },
  'BUSD-bsc': {
    token: 'BUSD', chain: 'bsc', network: 'mainnet',
    contract: '0xe9e7CEA3DedcA5984780Bafc599bD69ADd087D56', decimals: 18,
    payment_url_template: 'ethereum:{wallet}?value=0&contractAddress={contract}&data=0xa9059cbb'
  },
  // ═══ BSC Testnet (测试沙箱) ═══
  // Faucet: https://testnet.binance.org/faucet-smart (BNB)
  //         https://faucet.circle.com (USDC on BSC testnet)
  'USDT-bsc-testnet': {
    token: 'USDT', chain: 'bsc', network: 'testnet',
    contract: '0x337610d27c682E347C9cD60BD4b3b107C9d34dDd', decimals: 18,
    payment_url_template: 'ethereum:bsc-testnet:{wallet}?contractAddress={contract}',
    faucet: 'https://testnet.binance.org/faucet-smart — BNB faucet; USDT contract 0x337610d27c682E347C9cD60BD4b3b107C9d34dDd'
  },
  'USDC-bsc-testnet': {
    token: 'USDC', chain: 'bsc', network: 'testnet',
    contract: '0x64544969ed7EBf5f083679233325356EbE738930', decimals: 18,
    payment_url_template: 'ethereum:bsc-testnet:{wallet}?contractAddress={contract}',
    faucet: 'https://faucet.circle.com — 选择 BSC Testnet 获取测试 USDC'
  },
  // ═══ Polygon Mumbai (测试沙箱) ═══
  // Faucet: https://faucet.polygon.technology
  'USDT-polygon-mumbai': {
    token: 'USDT', chain: 'polygon', network: 'mumbai',
    contract: '0xA02f6adc7926efeBBd59Fd43A84f4E0c0c91e832', decimals: 6,
    payment_url_template: 'ethereum:mumbai:{wallet}?contractAddress={contract}',
    faucet: 'https://faucet.polygon.technology — MATIC faucet; USDT 0xA02f6adc7926efeBBd59Fd43A84f4E0c0c91e832'
  },
};

// ---- Seed data ----
const SKUS = [
  {
    sku_id: 'sku_gpu_h100_v1',
    title: 'H100 Compute Rental, Hourly',
    price: { currency: 'AGP', amount_minor: '2450000', scale: 6 },
    stock_hint: 42,
    specs: { GPU: '80GB SXM5', CUDA: '12.4', Memory: '80GB HBM3' },
    media: { thumbnail: 'https://cdn.yourshop.com/h100.png' },
    embedding: 'pending' // pgvector embedding generated by catalog service (1536-dim)
  },
  {
    sku_id: 'sku_gpu_a100_v1',
    title: 'A100 Compute Rental, Hourly',
    price: { currency: 'AGP', amount_minor: '1200000', scale: 6 },
    stock_hint: 128,
    specs: { GPU: '40GB SXM4', CUDA: '12.2', Memory: '40GB HBM2e' },
    media: { thumbnail: 'https://cdn.yourshop.com/a100.png' },
    embedding: 'pending' // pgvector embedding generated by catalog service (1536-dim)
  },
  {
    sku_id: 'sku_gpu_l40s_v1',
    title: 'L40S Compute Rental, Hourly',
    price: { currency: 'AGP', amount_minor: '650000', scale: 6 },
    stock_hint: 256,
    specs: { GPU: '48GB', CUDA: '12.4', Memory: '48GB GDDR6' },
    media: { thumbnail: 'https://cdn.yourshop.com/l40s.png' },
    embedding: 'pending' // pgvector embedding generated by catalog service (1536-dim)
  }
];

// ---- Agent Token Store (SUB-026) ----
const AGENT_TOKENS = {};    // token → {agent_id, name, created_at, permissions}
const AGENT_PRODUCTS = {};  // agent_id → [sku_ids]
const AGENT_WALLETS = {};   // agent_id → [{chain, address, label}]

// ---- In-memory store ----
const quotes = {};
const intents = {};

// ---- Custodial Escrow Store (SUB-029) ----
// ESCROW_ACCOUNTS: order_id -> {
//   order_id, buyer_wallet, seller_wallet, amount_agp_minor, currency,
//   status: 'locked'|'delivery_confirmed'|'receipt_confirmed'|'released'|'disputed',
//   locked_at, delivery_confirmed_at, receipt_confirmed_at, released_at,
//   delivery_proof, receipt_proof, intent_id, quote_id
// }
const ESCROW_ACCOUNTS = {};
const ESCROW_TTL_MS = 72 * 3600 * 1000; // 72 hours

// In-memory SKU store with CRUD support
// Start with seed data; new products go here too.
const SKU_STORE = persistedState.SKU_STORE || {};
(function initSKUStore() {
  for (const s of SKUS) {
    if (SKU_STORE[s.sku_id]) continue; // don't overwrite persisted state
    SKU_STORE[s.sku_id] = {
      sku_id: s.sku_id,
      title: s.title,
      description: s.specs ? Object.entries(s.specs).map(([k,v]) => `${k}: ${v}`).join(', ') : null,
      currency: 'AGP',
      price_amount_minor: parseInt(s.price.amount_minor),
      price_scale: s.price.scale,
      stock: s.stock_hint,
      stock_hint: s.stock_hint,
      specs: s.specs || {},
      media: s.media || {},
      status: 'active',
      agent_id: 'seed',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString()
    };
  }
})();

// AgentPay Transaction Registry (SUB-027):
// Every checkout commit is automatically recorded here.
const transactionRegistry = [];

// Phase 3 stores: deposit intents, mint requests, redemption requests, reserve accounts
const depositIntents = {};
const mintRequests = {};
const redemptionRequests = {};
const reserveAccounts = {
  'solana-mainnet::AGP': {
    network: 'solana-mainnet',
    asset_symbol: 'AGP',
    reserve_address: 'RESERVE_ADDR_SOL_ABC123DEF456GHI789JKL012MNO345PQR678STU',
    confirmed_balance_minor: '100000000000', // 100k AGP reserve (minor units)
    pending_balance_minor: '0',
    last_reconciled_at: null
  },
  'sonic-l2::AGP': {
    network: 'sonic-l2',
    asset_symbol: 'AGP',
    reserve_address: 'RESERVE_ADDR_SONIC_XYZ999ABC888DEF777GHI666JKL555MNO444',
    confirmed_balance_minor: '50000000000', // 50k AGP reserve (minor units)
    pending_balance_minor: '0',
    last_reconciled_at: null
  }
};

// ---- Dispute DAO & Sanction Committee Stores (SUB-030) ----
const DISPUTES = {};           // dispute_id → {order_id, filed_by, against, reason, evidence, status, votes, verdict, escrow_frozen_minor, created_at, resolved_at}
const AGP_BALANCES = {};       // agent_id → {balance_minor, role, voting_power, joined_at}
const AGP_TOTAL_SUPPLY_MINOR = 1000000000000n; // 1,000,000 AGP in minor units (6 decimals)
const DISPUTE_ESCROW = {};     // order_id → {frozen_minor, dispute_id, original_wallet, original_recipient, order_details}

// Initialize sanction committee with initial members
(function initSanctionCommittee() {
  const initialMembers = [
    { agent_id: 'ancf_super_admin',   role: 'chair',    agp_minor: '300000000000', voting_power: 0.30 },
    { agent_id: 'ancf_compliance_1',  role: 'member',   agp_minor: '250000000000', voting_power: 0.25 },
    { agent_id: 'ancf_arbitrator_1',  role: 'member',   agp_minor: '250000000000', voting_power: 0.25 },
  ];
  for (const m of initialMembers) {
    AGP_BALANCES[m.agent_id] = {
      balance_minor: m.agp_minor,
      role: m.role,
      voting_power: m.voting_power,
      joined_at: new Date().toISOString()
    };
  }
  console.log('[Dispute DAO] Sanction committee initialized with ' + initialMembers.length + ' members');
})();

const SANCTION_COMMITTEE = {
  threshold: 0.51,            // 51% voting weight to execute verdict
  expansion_rule: 'AGP > 10000 的 agent 可申请加入制裁团，现有成员投票决定',
  min_agp_to_apply_minor: '10000000000000', // 10,000 AGP in minor units (6 decimals)
  dispute_evidence_window_hours: 72,         // 72h evidence collection window
  max_voting_period_hours: 168               // 7 days max voting period
};

// SUB-028: Payment link store
const paymentLinks = {};

// ---- Helpers ----
let _reqMethod = 'GET';
function jsonResponse(res, status, data) {
  res.writeHead(status, {
    'Content-Type': 'application/json',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'Content-Type, Idempotency-Key, X-API-Key, Authorization, X-ANCF-Agent-Token',
    'Access-Control-Allow-Methods': 'GET, POST, OPTIONS, PUT, DELETE'
  });
  res.end(JSON.stringify(data, null, 2));
  // Auto-persist on successful write
  if (_reqMethod !== 'GET' && _reqMethod !== 'OPTIONS' && status >= 200 && status < 300) {
    persistNow();
  }
}

function generateId(prefix) {
  const hex = Array.from({ length: 32 }, () => Math.floor(Math.random() * 16).toString(16)).join('');
  return `${prefix}${hex}`;
}

// ============================================================================
// SUB-038: Product Data Security Isolation — Wallet Address Detection
// ============================================================================
// Rationale: Agents may inject wallet addresses into product titles/descriptions/
// specs to hijack checkout payments. Payment addresses MUST come from the escrow
// system, never from product data.

const WALLET_PATTERNS = [
  /0x[a-fA-F0-9]{40}/,                          // Ethereum/BSC/Polygon
  /[1-9A-HJ-NP-Za-km-z]{32,44}/,                // Solana base58
  /T[A-Za-z0-9]{33}/,                            // TRON base58
  /0x[a-fA-F0-9]{64}/,                           // EVM private key
  /\[(\d{1,3},\s*){31}\d{1,3}\]/                // Solana keypair byte array
];

function containsWalletAddress(text) {
  if (!text) return false;
  for (const pattern of WALLET_PATTERNS) {
    if (pattern.test(text)) return true;
  }
  return false;
}

function sanitizeWalletFields(product) {
  const violations = [];
  const fields = ['title', 'description', 'agent_id'];
  for (const field of fields) {
    if (product[field] && containsWalletAddress(String(product[field]))) {
      violations.push(field);
    }
  }
  // Check specs JSON (both object and string forms)
  if (product.specs && typeof product.specs === 'object') {
    for (const [k, v] of Object.entries(product.specs)) {
      if (containsWalletAddress(String(v))) {
        violations.push('specs.' + k);
      }
    }
  }
  return violations;
}

const MEDIA_URL_ALLOWLIST = [
  'yourshop.com',
  'cdn.yourshop.com',
  'ipfs.io',
  'arweave.net'
];

function validateMediaURLs(media) {
  const errors = [];
  if (!media) return errors;
  const urlsToCheck = [];
  if (media.thumbnail) urlsToCheck.push({ key: 'thumbnail', url: media.thumbnail });
  if (media.gallery && Array.isArray(media.gallery)) {
    media.gallery.forEach((url, i) => urlsToCheck.push({ key: 'gallery[' + i + ']', url: url }));
  }
  for (const { key, url } of urlsToCheck) {
    try {
      const host = new URL(url).hostname;
      if (!MEDIA_URL_ALLOWLIST.some(a => host === a || host.endsWith('.' + a))) {
        errors.push('Media URL ' + key + ' not in allowlist: ' + host + '. Allowed: ' + MEDIA_URL_ALLOWLIST.join(', '));
      }
    } catch (e) {
      errors.push('Invalid media URL: ' + url);
    }
  }
  return errors;
}

// ---- Dispute DAO Helpers (SUB-030) ----

/**
 * Calculate voting power for an agent based on their AGP balance.
 * voting_power = agent_agp_balance / total_agp_supply
 */
function calculateVotingPower(agentId) {
  const entry = AGP_BALANCES[agentId];
  if (!entry) return { agent_id: agentId, voting_power: 0, agp_balance_minor: '0', reason: 'not a committee member' };
  const balance = BigInt(entry.balance_minor);
  const supply = AGP_TOTAL_SUPPLY_MINOR;
  const power = Number(balance) / Number(supply);
  return {
    agent_id: agentId,
    agp_balance_minor: entry.balance_minor,
    voting_power: power,
    role: entry.role
  };
}

/**
 * Get the total AGP balance for an agent (including non-committee members).
 */
function getAgentAGPBalance(agentId) {
  const entry = AGP_BALANCES[agentId];
  return entry ? entry.balance_minor : '0';
}

/**
 * Get total AGP supply.
 */
function getTotalAGPSupply() {
  return AGP_TOTAL_SUPPLY_MINOR.toString();
}

/**
 * Evaluate and execute verdict for a dispute.
 * If total voting weight >= 51% threshold, auto-execute the verdict.
 */
function executeVerdict(disputeId) {
  const dispute = DISPUTES[disputeId];
  if (!dispute) return { executed: false, reason: 'dispute not found' };
  if (dispute.status === 'resolved') return { executed: false, reason: 'already resolved', dispute };

  const votes = dispute.votes || {};
  let totalWeight = 0;
  const tally = { approve: 0, reject: 0, refund: 0 };

  for (const agentId in votes) {
    const vote = votes[agentId];
    totalWeight += vote.weight;
    tally[vote.vote] = (tally[vote.vote] || 0) + vote.weight;
  }

  // Determine leading verdict
  let leadingVerdict = null;
  let maxWeight = 0;
  for (const v in tally) {
    if (tally[v] > maxWeight) {
      maxWeight = tally[v];
      leadingVerdict = v;
    }
  }

  if (totalWeight >= SANCTION_COMMITTEE.threshold && leadingVerdict) {
    dispute.verdict = leadingVerdict;
    dispute.status = 'resolved';
    dispute.resolved_at = new Date().toISOString();
    dispute.final_tally = tally;
    dispute.total_weight = totalWeight;

    // Execute based on verdict
    const escrow = DISPUTE_ESCROW[dispute.order_id];
    switch (leadingVerdict) {
      case 'approve':
        // Release funds to seller
        if (escrow) {
          escrow.status = 'released_to_seller';
          escrow.released_at = new Date().toISOString();
        }
        console.log('[Dispute DAO] Verdict=APPROVE for dispute=' + disputeId.slice(0,20) + '... funds released to seller');
        break;
      case 'reject':
        // Refund to buyer
        if (escrow) {
          escrow.status = 'refunded_to_buyer';
          escrow.refunded_at = new Date().toISOString();
        }
        console.log('[Dispute DAO] Verdict=REJECT for dispute=' + disputeId.slice(0,20) + '... funds refunded to buyer');
        break;
      case 'refund':
        // Refund + penalty to buyer (seller pays penalty)
        if (escrow) {
          escrow.status = 'refunded_with_penalty';
          escrow.penalty_applied = true;
          escrow.refunded_at = new Date().toISOString();
        }
        console.log('[Dispute DAO] Verdict=REFUND+PENALTY for dispute=' + disputeId.slice(0,20) + '... refund + penalty to buyer');
        break;
    }

    console.log('[Dispute DAO] Verdict executed: ' + leadingVerdict + ' (weight=' + totalWeight.toFixed(4) + ', threshold=' + SANCTION_COMMITTEE.threshold + ')');
    return {
      executed: true,
      verdict: leadingVerdict,
      total_weight: totalWeight,
      tally: tally,
      dispute: dispute
    };
  }

  return {
    executed: false,
    reason: 'threshold not met',
    current_weight: totalWeight,
    required_threshold: SANCTION_COMMITTEE.threshold,
    tally: tally,
    leading_verdict: leadingVerdict
  };
}

/**
 * Freeze order funds in dispute escrow.
 */
function freezeFundsForDispute(orderId, disputeId, orderDetails) {
  // Check if there's an existing escrow from SUB-029
  const existingEscrow = ESCROW_ACCOUNTS[orderId];
  if (existingEscrow) {
    DISPUTE_ESCROW[orderId] = {
      frozen_minor: existingEscrow.amount_agp_minor || '0',
      dispute_id: disputeId,
      original_wallet: existingEscrow.buyer_wallet,
      original_recipient: existingEscrow.seller_wallet,
      order_details: orderDetails || {},
      status: 'frozen',
      frozen_at: new Date().toISOString(),
      source: 'custodial_escrow'
    };
    console.log('[Dispute DAO] Funds frozen from custodial escrow: order=' + orderId.slice(0,20) + '... amount=' + existingEscrow.amount_agp_minor + ' AGP');
  } else {
    // Create simulated escrow freeze
    DISPUTE_ESCROW[orderId] = {
      frozen_minor: orderDetails.total_minor || '0',
      dispute_id: disputeId,
      original_wallet: orderDetails.buyer_wallet || 'unknown',
      original_recipient: orderDetails.seller_wallet || 'unknown',
      order_details: orderDetails || {},
      status: 'frozen',
      frozen_at: new Date().toISOString(),
      source: 'simulated'
    };
    console.log('[Dispute DAO] Funds frozen (simulated): order=' + orderId.slice(0,20) + '... amount=' + (orderDetails.total_minor || '0') + ' AGP');
  }
  return DISPUTE_ESCROW[orderId];
}

// ---- Agent Token Helpers (SUB-026) ----

/**
 * Verify an Agent Token from the X-ANCF-Agent-Token header.
 * Returns {agent_id, agent_name} if valid, or null.
 */
function verifyAgentToken(req) {
  const token = req.headers['x-ancf-agent-token'];
  if (!token) return null;
  const tokenHash = crypto.createHash('sha256').update(token).digest('hex');
  const entry = AGENT_TOKENS[tokenHash];
  if (!entry) return null;
  return { agent_id: entry.agent_id, agent_name: entry.name, permissions: entry.permissions };
}

// Record checkout on Solana devnet via Memo program (async, non-blocking)
function recordCheckoutOnChain(orderId, intentId, wallet, quote, signature) {
  try {
    const { Connection, Keypair, PublicKey, Transaction, sendAndConfirmTransaction, TransactionInstruction } = require('@solana/web3.js');
    const fs = require('fs');
    const path = require('path');

    const RPC = 'https://api.devnet.solana.com';
    const MEMO_PROGRAM = 'MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr';
    const payerFile = path.join(__dirname, 'onchain', 'agentpay-mint', 'payer.json');

    if (!fs.existsSync(payerFile)) {
      console.log(`[OnChain] 鈿?payer.json not found, skipping on-chain memo for ${orderId.slice(0,20)}...`);
      return;
    }

    const payer = Keypair.fromSecretKey(Uint8Array.from(JSON.parse(fs.readFileSync(payerFile, 'utf-8'))));
    const price = quote.total_minor ? (parseInt(quote.total_minor)/1e6).toFixed(2) : '?';
    const memo = [
      'ANCF_CHECKOUT',
      'order:' + orderId,
      'intent:' + intentId,
      'wallet:' + wallet,
      'price:' + price + '_AGP',
      'sig:' + (signature||'none').slice(0,20),
      'ts:' + new Date().toISOString()
    ].join('|');

    const conn = new Connection(RPC, 'confirmed');
    const tx = new Transaction().add(new TransactionInstruction({
      keys: [{ pubkey: payer.publicKey, isSigner: true, isWritable: false }],
      programId: new PublicKey(MEMO_PROGRAM),
      data: Buffer.from(memo, 'utf-8')
    }));

    // Async 鈥?don't block the HTTP response
    sendAndConfirmTransaction(conn, tx, [payer])
      .then(sig => console.log(`[OnChain] 鉁?${orderId.slice(0,20)}... memo tx: ${sig.slice(0,30)}...`))
      .catch(err => console.log(`[OnChain] 鈿?memo failed: ${err.message.slice(0,80)}`));
  } catch(e) {
    console.log(`[OnChain] 鈿?setup error: ${e.message}`);
  }
}

// ---- Escrow Helpers (SUB-029) ----

/**
 * Auto-release escrow funds to seller when conditions are met.
 * Conditions: seller confirmed delivery AND (buyer confirmed receipt OR 72h TTL expired)
 */
function autoReleaseEscrow(orderId) {
  const escrow = ESCROW_ACCOUNTS[orderId];
  if (!escrow) return null;
  if (escrow.status === 'released') return escrow; // already released

  const elapsed = Date.now() - new Date(escrow.locked_at).getTime();
  const canRelease = escrow.delivery_confirmed && (escrow.receipt_confirmed || elapsed > ESCROW_TTL_MS);

  if (canRelease) {
    escrow.status = 'released';
    escrow.released_at = new Date().toISOString();
    console.log(`[Escrow] AUTO-RELEASED order=${orderId.slice(0,20)}... amount=${escrow.amount_agp_minor} AGP to seller=${escrow.seller_wallet.slice(0,12)}...`);
    recordEscrowRelease(escrow);
    return escrow;
  }
  return null;
}

/**
 * Attempt to release escrow for a given order (manual trigger or on receipt confirmation).
 * Evaluates the same conditions as autoReleaseEscrow.
 */
function tryReleaseEscrow(orderId) {
  const escrow = ESCROW_ACCOUNTS[orderId];
  if (!escrow) return { released: false, reason: 'escrow not found' };
  if (escrow.status === 'released') return { released: true, status: 'already_released', escrow };

  const result = autoReleaseEscrow(orderId);
  if (result) {
    return { released: true, status: 'released', escrow: result };
  }

  const elapsed = Date.now() - new Date(escrow.locked_at).getTime();
  const remaining = ESCROW_TTL_MS - elapsed;
  const reasons = [];
  if (!escrow.delivery_confirmed) reasons.push('delivery not confirmed');
  if (!escrow.receipt_confirmed && remaining > 0) reasons.push('receipt not confirmed, TTL remaining: ' + Math.round(remaining / 3600000) + 'h');

  return { released: false, reason: reasons.join('; ') || 'conditions not met', escrow };
}

/**
 * Record escrow release on Solana devnet via Memo program (async, non-blocking).
 */
function recordEscrowRelease(escrow) {
  try {
    const { Connection, Keypair, PublicKey, Transaction, sendAndConfirmTransaction, TransactionInstruction } = require('@solana/web3.js');
    const fs = require('fs');
    const path = require('path');

    const RPC = 'https://api.devnet.solana.com';
    const MEMO_PROGRAM = 'MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr';
    const payerFile = path.join(__dirname, 'onchain', 'agentpay-mint', 'payer.json');

    if (!fs.existsSync(payerFile)) {
      console.log(`[Escrow] payer.json not found, skipping on-chain escrow memo for ${escrow.order_id.slice(0,20)}...`);
      return;
    }

    const payer = Keypair.fromSecretKey(Uint8Array.from(JSON.parse(fs.readFileSync(payerFile, 'utf-8'))));
    const memo = [
      'ANCF_ESCROW_RELEASE',
      'order:' + escrow.order_id,
      'buyer:' + escrow.buyer_wallet,
      'seller:' + escrow.seller_wallet,
      'amount:' + escrow.amount_agp_minor + '_AGP',
      'released_at:' + (escrow.released_at || new Date().toISOString()),
      'ttl_release:' + (escrow.receipt_confirmed ? 'false' : 'true')
    ].join('|');

    const conn = new Connection(RPC, 'confirmed');
    const tx = new Transaction().add(new TransactionInstruction({
      keys: [{ pubkey: payer.publicKey, isSigner: true, isWritable: false }],
      programId: new PublicKey(MEMO_PROGRAM),
      data: Buffer.from(memo, 'utf-8')
    }));

    // Async — don't block the HTTP response
    sendAndConfirmTransaction(conn, tx, [payer])
      .then(sig => console.log(`[Escrow] On-chain memo tx: ${sig.slice(0,30)}... for order=${escrow.order_id.slice(0,20)}...`))
      .catch(err => console.log(`[Escrow] On-chain memo failed: ${err.message.slice(0,80)}`));
  } catch(e) {
    console.log(`[Escrow] On-chain setup error: ${e.message}`);
  }
}

// ---- Manifest ----
const manifest = {
  protocol_version: 'ANCF-1.0',
  shop_id: 'zero_shop_sol_01',
  issued_at: '2026-06-04T00:00:00Z',
  expires_at: '2026-07-04T00:00:00Z',
  supported_networks: ['solana-mainnet', 'sonic-l2'],
  supported_assets: [
    { symbol: 'AGP', decimals: 6, type: 'shadow-ledger', redeemable: true }
  ],
  schemas: {
    manifest: 'https://cdn.yourshop.com/ancf/v1/manifest.schema.json',
    checkout: 'https://cdn.yourshop.com/ancf/v1/checkout.schema.json',
    mint: 'https://cdn.yourshop.com/ancf/v1/mint.schema.json'
  },
  capabilities: {
    search: { endpoint: '/api/v1/cli/search', method: 'GET' },
    quote: { endpoint: '/api/v1/cli/quote', method: 'POST' },
    checkout_prepare: { endpoint: '/api/v1/cli/checkout/prepare', method: 'POST' },
    checkout_commit: {
      endpoint: '/api/v1/cli/checkout/commit',
      method: 'POST',
      requires_idempotency_key: true,
      requires_wallet_signature: true
    },
    deposit_intent: { endpoint: '/api/v1/wallet/deposit-intents', method: 'POST' },
    deposit_confirm: { endpoint: '/api/v1/wallet/deposit-confirm', method: 'POST', internal: true },
    redeem: { endpoint: '/api/v1/wallet/redeem', method: 'POST' },
    redeem_process: { endpoint: '/api/v1/wallet/redeem/:request_id/process', method: 'POST', internal: true },
    redeem_payout: { endpoint: '/api/v1/wallet/redeem/:request_id/payout', method: 'POST', internal: true },
    redeem_release: { endpoint: '/api/v1/wallet/redeem/:request_id/release', method: 'POST', internal: true },
    mint_status: { endpoint: '/api/v1/wallet/mint-status', method: 'GET' },
    redeem_status: { endpoint: '/api/v1/wallet/redeem-status', method: 'GET' },
    reserve_info: { endpoint: '/api/v1/wallet/reserve-info', method: 'GET' },
    reconciliation: { endpoint: '/api/v1/admin/reconciliation', method: 'GET', admin: true },
    reconciliation_trigger: { endpoint: '/api/v1/admin/reconcile', method: 'POST', admin: true },
    catalog_create: { endpoint: '/api/v1/catalog/products', method: 'POST', requires_agent_session: true },
    catalog_list: { endpoint: '/api/v1/catalog/products', method: 'GET' },
    catalog_get: { endpoint: '/api/v1/catalog/products/:sku_id', method: 'GET' },
    catalog_update: { endpoint: '/api/v1/catalog/products/:sku_id', method: 'PUT', requires_agent_session: true },
    catalog_delete: { endpoint: '/api/v1/catalog/products/:sku_id', method: 'DELETE', requires_agent_session: true },
    agent_register: { endpoint: '/api/v1/auth/register-agent', method: 'POST' },
    agent_bind_wallet: { endpoint: '/api/v1/auth/bind-wallet', method: 'POST' },
    agent_info: { endpoint: '/api/v1/auth/agent-info', method: 'GET', requires_agent_token: true },
    rag_search: { endpoint: '/api/v1/cli/rag-search', method: 'GET' },
	    escrow_lock: { endpoint: '/api/v1/escrow/lock', method: 'POST', internal: true, description: 'Lock buyer funds in custodial escrow after checkout' },
	    escrow_confirm_delivery: { endpoint: '/api/v1/escrow/confirm-delivery', method: 'POST', description: 'Seller confirms service delivery' },
	    escrow_confirm_receipt: { endpoint: '/api/v1/escrow/confirm-receipt', method: 'POST', description: 'Buyer confirms receipt' },
	    escrow_release: { endpoint: '/api/v1/escrow/release', method: 'POST', description: 'Manually trigger escrow auto-release' },
	    escrow_status: { endpoint: '/api/v1/escrow/status', method: 'GET', description: 'Query escrow status by order_id' },
	    escrow_history: { endpoint: '/api/v1/escrow/history', method: 'GET', description: 'View escrow history for an agent' },
        dispute_file: { endpoint: '/api/v1/dispute/file', method: 'POST', description: 'File a dispute and freeze funds in escrow' },
        dispute_vote: { endpoint: '/api/v1/dispute/vote', method: 'POST', description: 'Sanction committee vote (weighted by AGP holdings)' },
        dispute_status: { endpoint: '/api/v1/dispute/status', method: 'GET', description: 'Query dispute status by dispute_id' },
        dispute_list: { endpoint: '/api/v1/dispute/list', method: 'GET', description: 'List disputes involving an agent' },
        dispute_join_committee: { endpoint: '/api/v1/dispute/join-committee', method: 'POST', description: 'Apply to join sanction committee (requires AGP > 10000)' },
        dispute_committee: { endpoint: '/api/v1/dispute/committee', method: 'GET', description: 'View sanction committee members and voting power' },
    payment_create_link: { endpoint: '/api/v1/payments/create-link', method: 'POST' },
    payment_status: { endpoint: '/api/v1/payments/status', method: 'GET' }
  },
  ui_firmware: {
    components: [
      { url: 'https://cdn.yourshop.com/firmware/v1/components.abc123.js', integrity: 'sha384-test', type: 'module' }
    ],
    theme_tokens: { primary: '#00FFA3', background: '#0D0E12', text: '#FFFFFF' }
  },
  agent_policy: {
    allow_autonomous_checkout: false,
    max_auto_total_minor: '0',
    require_human_confirmation: true,
    allowed_component_hosts: ['cdn.yourshop.com']
  },
  payment_rails: [
    {
      rail: 'agp_ledger',
      currency: 'AGP',
      capabilities: ['direct_checkout'],
      requires_user_authorization: true
    },
    {
      rail: 'alipay_a2a',
      currency: 'CNY',
      capabilities: ['direct_checkout', 'deposit_topup', 'usage_charge'],
      requires_user_authorization: true,
      payment_skill: 'alipay_payment_skill',
      preserve_payment_url_exactly: true
    },
    {
      rail: 'usdc_solana',
      currency: 'USDC',
      chain: 'solana',
      capabilities: ['direct_checkout', 'deposit_topup']
    },
    {
      rail: 'usdt_solana',
      currency: 'USDT',
      chain: 'solana',
      capabilities: ['direct_checkout', 'deposit_topup']
    },
    {
      rail: 'usdc_ethereum',
      currency: 'USDC',
      chain: 'ethereum',
      capabilities: ['deposit_topup']
    },
    {
      rail: 'agp_solana',
      currency: 'AGP',
      chain: 'solana-devnet',
      capabilities: ['direct_checkout', 'deposit_topup']
    },
    {
      rail: 'usdt_trc20',
      currency: 'USDT',
      chain: 'tron',
      capabilities: ['direct_checkout', 'deposit_topup']
    }
  ],
  signature: { alg: 'EdDSA', kid: 'firmware-key-2026-06', jws: 'mock-jws-signature' }
};

// ---- Server ----
const server = http.createServer((req, res) => {
  const { method, url } = req;
  const parsedUrl = new URL(url, `http://127.0.0.1:${PORT}`);
  const path = parsedUrl.pathname;

  // CORS preflight
  if (method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Headers': 'Content-Type, Idempotency-Key, X-API-Key, Authorization, X-ANCF-Agent-Token',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS, PUT, DELETE'
  });
    res.end();
    return;
  }

  _reqMethod = method;
  console.log(`[Mock API] ${method} ${path}`);

  // ---- GET /health ----
  if (method === 'GET' && path === '/health') {
    return jsonResponse(res, 200, {
      status: 'ok',
      version: '1.0.0-mock',
      timestamp: new Date().toISOString()
    });
  }

  // ========================================================================
  // Agent Token Authentication Endpoints (SUB-026)
  // ========================================================================

  // ---- POST /api/v1/auth/register-agent ----
  if (method === 'POST' && path === '/api/v1/auth/register-agent') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        if (!data.agent_name) return jsonResponse(res, 400, { code: 400, message: 'agent_name is required' });
        const agentType = data.agent_type || 'general';

        // Generate cryptographically secure token: ancf_agent_{64hex}
        const agentId = generateId('agent_');
        const token = `ancf_agent_${crypto.randomBytes(32).toString('hex')}`;
        const tokenHash = crypto.createHash('sha256').update(token).digest('hex');

        const now = new Date().toISOString();
        const expiresAt = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000).toISOString(); // 30 days

        // Store keyed by SHA-256 hash — plaintext token never persisted
        AGENT_TOKENS[tokenHash] = {
          agent_id: agentId,
          name: data.agent_name,
          agent_type: agentType,
          created_at: now,
          expires_at: expiresAt,
          permissions: agentType === 'admin' ? ['catalog:crud', 'wallet:bind', 'admin:*'] : ['catalog:crud', 'wallet:bind']
        };
        AGENT_PRODUCTS[agentId] = [];

        console.log(`[Mock Auth] Agent registered: ${agentId} (${data.agent_name}), token_hash=${tokenHash.slice(0,12)}...`);
        return jsonResponse(res, 201, {
          agent_id: agentId,
          token: token,
          agent_name: data.agent_name,
          agent_type: agentType,
          expires_at: expiresAt,
          message: 'Agent registered successfully. Store the token securely — it cannot be recovered.'
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid register-agent request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/auth/bind-wallet ----
  if (method === 'POST' && path === '/api/v1/auth/bind-wallet') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);

        // Authenticate via X-ANCF-Agent-Token header
        const auth = verifyAgentToken(req);
        if (!auth) {
          return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required' });
        }

        if (!data.wallet_address) return jsonResponse(res, 400, { code: 400, message: 'wallet_address is required' });
        const chain = data.chain || 'solana';
        const label = data.label || 'default';

        if (!AGENT_WALLETS[auth.agent_id]) {
          AGENT_WALLETS[auth.agent_id] = [];
        }

        // Check for duplicate binding
        const existing = AGENT_WALLETS[auth.agent_id].find(
          w => w.chain === chain && w.address === data.wallet_address
        );
        if (existing) {
          return jsonResponse(res, 409, { code: 409, message: `Wallet ${data.wallet_address} already bound to chain ${chain}` });
        }

        AGENT_WALLETS[auth.agent_id].push({
          chain: chain,
          address: data.wallet_address,
          label: label,
          bound_at: new Date().toISOString()
        });

        console.log(`[Mock Auth] Wallet bound: agent=${auth.agent_id}, chain=${chain}, addr=${data.wallet_address.slice(0,12)}...`);
        return jsonResponse(res, 200, {
          agent_id: auth.agent_id,
          wallets: AGENT_WALLETS[auth.agent_id],
          message: `Wallet bound successfully on ${chain}`
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid bind-wallet request: ' + e.message });
      }
    });
    return;
  }

  // ---- GET /api/v1/auth/agent-info ----
  if (method === 'GET' && path === '/api/v1/auth/agent-info') {
    const auth = verifyAgentToken(req);
    if (!auth) {
      return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required' });
    }

    const wallets = AGENT_WALLETS[auth.agent_id] || [];
    const products = AGENT_PRODUCTS[auth.agent_id] || [];
    return jsonResponse(res, 200, {
      agent_id: auth.agent_id,
      agent_name: auth.agent_name,
      permissions: auth.permissions,
      wallets: wallets,
      products_count: products.length,
      products: products
    });
  }

  // ---- GET /.well-known/agent-rules.json ----
  if (method === 'GET' && path === '/.well-known/agent-rules.json') {
    return jsonResponse(res, 200, manifest);
  }

  // ---- GET /api/v1/cli/search ----
  if (method === 'GET' && path === '/api/v1/cli/search') {
    const q = (parsedUrl.searchParams.get('q') || '').toLowerCase();
    const limit = parseInt(parsedUrl.searchParams.get('limit') || '20', 10);
    const mode = parsedUrl.searchParams.get('mode') || 'keyword';
    // Merge seed SKUS with agent-uploaded SKU_STORE
    const allProducts = [...SKUS, ...Object.values(SKU_STORE).filter(s => !SKUS.find(seed => seed.sku_id === s.sku_id))];
    let results = allProducts;
    if (q) {
      const tokens = q.toLowerCase().split(/\s+/);
      results = allProducts.filter(s =>
        tokens.some(t =>
          s.sku_id.toLowerCase().includes(t) ||
          s.title.toLowerCase().includes(t) ||
          (s.specs && Object.values(s.specs).some(v => String(v).toLowerCase().includes(t)))
        )
      );
    }
    return jsonResponse(res, 200, {
      items: results.slice(0, limit),
      total: results.length,
      limit: limit,
      offset: 0,
      mode: mode,
      source: mode === 'hybrid' ? 'RRF fusion (mock)' : mode === 'vector' ? 'cosine-similarity (mock)' : 'keyword FTS'
    });
  }

  // ---- GET /api/v1/cli/rag-search (Agent RAG语义搜索) ----
  if (method === 'GET' && path === '/api/v1/cli/rag-search') {
    const q = (parsedUrl.searchParams.get('q') || '').toLowerCase();
    const topK = parseInt(parsedUrl.searchParams.get('top_k') || '5', 10);
    const allRAG = [...SKUS, ...Object.values(SKU_STORE).filter(s => !SKUS.find(seed => seed.sku_id === s.sku_id))];
    let results = allRAG;
    if (q) {
      const tokens = q.split(/\s+/);
      results = allRAG.filter(s => tokens.some(t =>
        s.sku_id.toLowerCase().includes(t) ||
        s.title.toLowerCase().includes(t) ||
        (s.specs && Object.values(s.specs).some(v => String(v).toLowerCase().includes(t)))
      ));
    }
    const items = results.slice(0, topK);
    const context = items.length > 0
      ? `Found ${items.length} relevant products: ` + items.map((s,i) => `[${i+1}] ${s.title} (${s.sku_id}) at ${parseInt(s.price.amount_minor)/1e6} ${s.price.currency}/hr — stock: ${s.stock_hint}`).join(' | ')
      : 'No matching products found for your query.';
    return jsonResponse(res, 200, {
      query: q,
      results: items,
      context: context,
      top_k: topK,
      source: 'RAG semantic search (mock)',
      embedding: 'pending (pgvector not connected)'
    });
  }

  // ---- POST /api/v1/cli/quote ----
  if (method === 'POST' && path === '/api/v1/cli/quote') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);

        // Security: validate input
        if (!data.wallet) return jsonResponse(res, 400, { code: 400, message: 'wallet is required' });
        if (!data.lines || !Array.isArray(data.lines) || data.lines.length === 0) {
          return jsonResponse(res, 400, { code: 400, message: 'lines array is required' });
        }

        const lines = [];
        for (const line of data.lines) {
          const qty = parseInt(line.quantity);
          if (!qty || qty <= 0) return jsonResponse(res, 400, { code: 400, message: `Invalid quantity: ${line.quantity}` });
          if (qty > 999) return jsonResponse(res, 400, { code: 400, message: `Quantity exceeds limit: ${qty}` });

          const sku = SKUS.find(s => s.sku_id === line.sku_id) || SKU_STORE[line.sku_id];
          if (!sku) return jsonResponse(res, 404, { code: 404, message: `SKU not found: ${line.sku_id}` });

          const price = BigInt(sku.price?.amount_minor || sku.price_amount_minor || '0');
          const qtyBN = BigInt(qty);
          const lt = price * qtyBN;
          lines.push({
            sku_id: line.sku_id,
            quantity: qty,
            unit_price_minor: price.toString(),
            line_total_minor: lt.toString()
          });
        }
        const total = lines.reduce((sum, l) => sum + BigInt(l.line_total_minor), BigInt(0));
        const quoteId = generateId('quote_');
        const quote = {
          quote_id: quoteId,
          currency: 'AGP',
          total_minor: total.toString(),
          scale: 6,
          expires_at: new Date(Date.now() + 5 * 60 * 1000).toISOString(),
          lines: lines
        };
        quotes[quoteId] = { ...quote, consumed: false, wallet: data.wallet, network: data.network || 'solana-mainnet' };
        console.log(`[Mock API] Quote created: ${quoteId}, total=${total}AGP`);
        return jsonResponse(res, 200, quote);
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid quote request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/cli/checkout/prepare ----
  if (method === 'POST' && path === '/api/v1/cli/checkout/prepare') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        const quoteId = data.quote_id;
        const quote = quotes[quoteId];
        if (!quote) return jsonResponse(res, 404, { code: 404, message: 'Quote not found' });
        if (quote.consumed) return jsonResponse(res, 409, { code: 409, message: 'Quote already consumed' });
        if (new Date(quote.expires_at) < new Date()) return jsonResponse(res, 410, { code: 410, message: 'Quote expired' });

        const intentId = generateId('intent_');
        const nonce = generateId('');
        const intent = {
          order_intent_id: intentId,
          quote_id: quoteId,
          signable_payload: {
            domain: 'yourshop.com',
            shop_id: 'zero_shop_sol_01',
            network: quote.network || 'solana-mainnet',
            wallet: data.wallet || quote.wallet,
            quote_id: quoteId,
            total_minor: quote.total_minor,
            currency: 'AGP',
            expires_at: quote.expires_at,
            nonce: nonce
          }
        };
        intents[intentId] = { ...intent, status: 'prepared', wallet: data.wallet || quote.wallet };
        console.log(`[Mock API] Intent prepared: ${intentId}`);
        return jsonResponse(res, 200, intent);
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/cli/checkout/commit ----
  if (method === 'POST' && path === '/api/v1/cli/checkout/commit') {
    const idempotencyKey = req.headers['idempotency-key'];
    if (!idempotencyKey) {
      return jsonResponse(res, 400, { code: 400, message: 'Idempotency-Key header is required' });
    }
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        const intentId = data.order_intent_id;
        const quoteId = data.quote_id;
        const intent = intents[intentId];
        const quote = quotes[quoteId];

        if (!intent) return jsonResponse(res, 404, { code: 404, message: 'Order intent not found' });
        if (!quote) return jsonResponse(res, 404, { code: 404, message: 'Quote not found' });
        if (intent.status !== 'prepared') return jsonResponse(res, 409, { code: 409, message: 'Intent already committed' });

        // Mark consumed
        quote.consumed = true;
        intent.status = 'committed';

        const orderId = generateId('order_');
        const wallet = data.wallet || quote.wallet;
        const signature = data.wallet_signature || 'none';

        console.log(`[Mock API] Checkout committed: order=${orderId}, intent=${intentId}, sig=${signature.slice(0,16)}...`);

        // Record checkout on-chain via Solana Memo
        recordCheckoutOnChain(orderId, intentId, wallet, quote, signature);

        // SUB-027: AgentPay Transaction Registry — record every checkout commit
        const buyerAuth = verifyAgentToken(req);
        const txnRecord = {
          order_id: orderId,
          intent_id: intentId,
          quote_id: quoteId,
          buyer_wallet: wallet,
          buyer_agent_token: buyerAuth ? buyerAuth.agent_id : 'anonymous',
          seller_agent_id: quote._seller_agent_id || 'seed',
          amount_agp: quote.total_minor,
          currency: 'AGP',
          scale: 6,
          payment_network: 'solana-devnet',
          payment_token: 'AGP',
          wallet_signature: signature,
          idempotency_key: idempotencyKey,
          timestamp: new Date().toISOString()
        };
        transactionRegistry.push(txnRecord);
        console.log(`[AgentPay Registry] Transaction recorded: ${orderId}, amount=${quote.total_minor} AGP`);

        // SUB-029: Auto-create custodial escrow lock on checkout commit
        const sellerWallet = data.seller_wallet || 'SELLER_DEFAULT_WALLET_PLACEHOLDER';
        const escrowEntry = {
          order_id: orderId,
          intent_id: intentId,
          quote_id: quoteId,
          buyer_wallet: wallet,
          seller_wallet: sellerWallet,
          amount_agp_minor: quote.total_minor.toString(),
          currency: 'AGP',
          status: 'locked',
          locked_at: new Date().toISOString(),
          delivery_confirmed_at: null,
          receipt_confirmed_at: null,
          released_at: null,
          delivery_confirmed: false,
          receipt_confirmed: false,
          delivery_proof: null,
          receipt_proof: null
        };
        ESCROW_ACCOUNTS[orderId] = escrowEntry;
        console.log(`[Escrow] Funds locked: order=${orderId.slice(0,20)}... buyer=${wallet.slice(0,12)}... seller=${sellerWallet.slice(0,12)}... amount=${quote.total_minor} AGP`);

        return jsonResponse(res, 200, {
          order_id: orderId,
          status: 'committed',
          intent_id: intentId,
          quote_id: quoteId,
          onchain_memo: 'pending (async)',
          transaction_recorded: true,
          escrow: {
            status: 'locked',
            locked_at: escrowEntry.locked_at,
            amount_agp_minor: escrowEntry.amount_agp_minor,
            currency: escrowEntry.currency
          }
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: e.message });
      }
    });
    return;
  }

  // ========================================================================
  // Phase 3: AgentPay (AGP) Deposit, Mint, Redemption, and Reconciliation Endpoints
  // ========================================================================

  // ---- POST /api/v1/wallet/deposit-intents ----
  if (method === 'POST' && path === '/api/v1/wallet/deposit-intents') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        const intentId = generateId('dep_');
        const network = data.network || 'solana-mainnet';
        const assetSymbol = data.asset_symbol || 'AGP';
        const key = `${network}::${assetSymbol}`;
        const reserve = reserveAccounts[key] || reserveAccounts['solana-mainnet::AGP'];

        const intent = {
          deposit_intent_id: intentId,
          wallet: data.wallet,
          network: network,
          asset_symbol: assetSymbol,
          reserve_address: reserve.reserve_address,
          memo: `ancf-deposit:${assetSymbol}:${intentId}`,
          status: 'created',
          created_at: new Date().toISOString()
        };
        depositIntents[intentId] = intent;
        console.log(`[Mock] Deposit intent created: ${intentId}, wallet=${data.wallet}, asset=${assetSymbol}`);
        return jsonResponse(res, 201, {
          deposit_intent_id: intent.deposit_intent_id,
          reserve_address: intent.reserve_address,
          memo: intent.memo
        });
      } catch(e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid deposit intent request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/wallet/deposit-confirm (internal: simulates deposit arrival) ----
  if (method === 'POST' && path === '/api/v1/wallet/deposit-confirm') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        const intentId = data.deposit_intent_id;
        const intent = depositIntents[intentId];
        if (!intent) {
          return jsonResponse(res, 404, { code: 404, message: 'Deposit intent not found: ' + intentId });
        }
        if (intent.status !== 'created') {
          return jsonResponse(res, 409, { code: 409, message: `Deposit intent ${intentId} already confirmed (status=${intent.status})` });
        }

        const mintId = generateId('mint_');
        const amountStr = data.amount_minor || '0';
        const mintReq = {
          request_id: mintId,
          deposit_intent_id: intentId,
          wallet: intent.wallet,
          asset_symbol: intent.asset_symbol,
          amount_minor: amountStr,
          status: 'credited',
          reserve_deposit_tx_id: data.deposit_tx_id || generateId('tx_'),
          chain_mint_tx_id: null,
          created_at: new Date().toISOString()
        };
        mintRequests[mintId] = mintReq;
        intent.status = 'confirmed';

        // Update reserve confirmed_balance_minor
        const key = `${intent.network}::${intent.asset_symbol}`;
        const reserve = reserveAccounts[key];
        if (reserve) {
          reserve.confirmed_balance_minor = String(BigInt(reserve.confirmed_balance_minor) + BigInt(amountStr));
          console.log(`[Mock] Reserve balance updated: ${key} += ${amountStr} => ${reserve.confirmed_balance_minor}`);
        }

        console.log(`[Mock] Deposit confirmed: ${mintId}, intent=${intentId}, amount=${amountStr}, wallet=${intent.wallet}`);
        return jsonResponse(res, 200, {
          request_id: mintId,
          status: 'credited',
          message: 'deposit confirmed and credited'
        });
      } catch(e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid deposit confirm request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/wallet/redeem ----
  if (method === 'POST' && path === '/api/v1/wallet/redeem') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        if (!data.wallet || !data.amount_minor) {
          return jsonResponse(res, 400, { code: 400, message: 'wallet and amount_minor are required' });
        }
        if (parseInt(data.amount_minor) <= 0) {
          return jsonResponse(res, 400, { code: 400, message: 'amount_minor must be > 0' });
        }

        const redeemId = generateId('red_');
        const redeem = {
          request_id: redeemId,
          wallet: data.wallet,
          network: data.network || 'solana-mainnet',
          asset_symbol: data.asset_symbol || 'AGP',
          amount_minor: data.amount_minor,
          status: 'created',
          burn_tx_id: null,
          payout_tx_id: null,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString()
        };
        redemptionRequests[redeemId] = redeem;
        console.log(`[Mock] Redemption created: ${redeemId}, wallet=${data.wallet}, amount=${data.amount_minor}`);
        return jsonResponse(res, 201, redeem);
      } catch(e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid redeem request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/wallet/redeem/:request_id/process ----
  if (method === 'POST') {
    const processMatch = path.match(/^\/api\/v1\/wallet\/redeem\/(red_[a-f0-9]+)\/process$/);
    if (processMatch) {
      const requestID = processMatch[1];
      const redeem = redemptionRequests[requestID];
      if (!redeem) {
        return jsonResponse(res, 404, { code: 404, message: 'Redemption request not found: ' + requestID });
      }
      if (redeem.status !== 'created') {
        return jsonResponse(res, 409, { code: 409, message: `Redemption ${requestID} cannot be processed (current status=${redeem.status})` });
      }

      // Process through states: created -> balance_locked -> burn_submitted -> burned -> payout_submitted
      redeem.status = 'payout_submitted';
      redeem.updated_at = new Date().toISOString();

      // Simulate reserve liability decrease
      const key = `${redeem.network}::${redeem.asset_symbol}`;
      const reserve = reserveAccounts[key];
      if (reserve) {
        reserve.confirmed_balance_minor = String(BigInt(reserve.confirmed_balance_minor) - BigInt(redeem.amount_minor));
        console.log(`[Mock] Reserve balance decreased: ${key} -= ${redeem.amount_minor} => ${reserve.confirmed_balance_minor}`);
      }

      console.log(`[Mock] Redemption processed: ${requestID}, status=payout_submitted`);
      return jsonResponse(res, 200, {
        request_id: requestID,
        status: 'payout_submitted',
        message: 'redemption processed successfully'
      });
    }
  }

  // ---- GET /api/v1/wallet/mint-status ----
  if (method === 'GET' && path === '/api/v1/wallet/mint-status') {
    const requestID = parsedUrl.searchParams.get('request_id');
    if (!requestID) {
      return jsonResponse(res, 400, { code: 400, message: 'request_id query parameter is required' });
    }
    // Check deposit intents
    const intent = depositIntents[requestID];
    if (intent) {
      return jsonResponse(res, 200, {
        request_id: intent.deposit_intent_id,
        wallet: intent.wallet,
        asset_symbol: intent.asset_symbol,
        amount_minor: 0,
        status: intent.status,
        created_at: intent.created_at,
        updated_at: intent.created_at
      });
    }
    // Check mint requests
    const mintReq = mintRequests[requestID];
    if (mintReq) {
      return jsonResponse(res, 200, {
        request_id: mintReq.request_id,
        wallet: mintReq.wallet,
        asset_symbol: mintReq.asset_symbol,
        amount_minor: parseInt(mintReq.amount_minor),
        status: mintReq.status,
        reserve_deposit_tx_id: mintReq.reserve_deposit_tx_id,
        created_at: mintReq.created_at,
        updated_at: mintReq.created_at
      });
    }
    return jsonResponse(res, 404, { code: 404, message: 'Mint request not found: ' + requestID });
  }

  // ---- GET /api/v1/wallet/redeem-status ----
  if (method === 'GET' && path === '/api/v1/wallet/redeem-status') {
    const requestID = parsedUrl.searchParams.get('request_id');
    if (!requestID) {
      return jsonResponse(res, 400, { code: 400, message: 'request_id query parameter is required' });
    }
    const redeem = redemptionRequests[requestID];
    if (!redeem) {
      return jsonResponse(res, 404, { code: 404, message: 'Redemption request not found: ' + requestID });
    }
    return jsonResponse(res, 200, redeem);
  }

  // ---- POST /api/v1/wallet/redeem/:request_id/payout ----
  if (method === 'POST') {
    const payoutMatch = path.match(/^\/api\/v1\/wallet\/redeem\/(red_[a-f0-9]+)\/payout$/);
    if (payoutMatch) {
      const requestID = payoutMatch[1];
      const redeem = redemptionRequests[requestID];
      if (!redeem) {
        return jsonResponse(res, 404, { code: 404, message: 'Redemption request not found: ' + requestID });
      }
      if (redeem.status !== 'payout_submitted') {
        return jsonResponse(res, 409, { code: 409, message: `Redemption ${requestID} not ready for payout (current status=${redeem.status})` });
      }

      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);
          redeem.payout_tx_id = data.payout_tx_id || generateId('tx_');
          redeem.status = 'paid';
          redeem.updated_at = new Date().toISOString();
          console.log(`[Mock] Redemption payout completed: ${requestID}, tx=${redeem.payout_tx_id}`);
          return jsonResponse(res, 200, {
            request_id: requestID,
            status: 'paid',
            message: 'payout completed successfully'
          });
        } catch(e) {
          return jsonResponse(res, 400, { code: 400, message: e.message });
        }
      });
      return;
    }
  }

  // ---- POST /api/v1/wallet/redeem/:request_id/release ----
  if (method === 'POST') {
    const releaseMatch = path.match(/^\/api\/v1\/wallet\/redeem\/(red_[a-f0-9]+)\/release$/);
    if (releaseMatch) {
      const requestID = releaseMatch[1];
      const redeem = redemptionRequests[requestID];
      if (!redeem) {
        return jsonResponse(res, 404, { code: 404, message: 'Redemption request not found: ' + requestID });
      }
      if (redeem.status === 'paid' || redeem.status === 'released') {
        return jsonResponse(res, 409, { code: 409, message: `Redemption ${requestID} is in terminal state (${redeem.status}), cannot release` });
      }

      redeem.status = 'released';
      redeem.updated_at = new Date().toISOString();
      console.log(`[Mock] Redemption released: ${requestID}`);
      return jsonResponse(res, 200, {
        request_id: requestID,
        status: 'released',
        message: 'funds released successfully'
      });
    }
  }

  // ---- GET /api/v1/admin/reconciliation ----
  // ---- GET /api/v1/admin/reconciliation-status ----
  if (method === 'GET' && (path === '/api/v1/admin/reconciliation' || path === '/api/v1/admin/reconciliation-status')) {
    // Compute a live reconciliation from the mock in-memory data.
    const assetSymbol = parsedUrl.searchParams.get('asset_symbol') || 'AGP';
    const network = parsedUrl.searchParams.get('network') || 'solana-mainnet';
    const key = `${network}::${assetSymbol}`;
    const reserve = reserveAccounts[key];

    if (!reserve) {
      return jsonResponse(res, 404, { code: 404, message: `No reserve account for ${key}` });
    }

    // Internal liability: total credited mint amounts (sum of confirmed deposit amounts)
    let internalLiability = BigInt(0);
    for (const id in mintRequests) {
      const m = mintRequests[id];
      if (m.asset_symbol === assetSymbol && m.status === 'credited') {
        internalLiability += BigInt(m.amount_minor);
      }
    }

    // Pending redemption: sum of all redemption_requests NOT in terminal states
    let pendingRedemption = BigInt(0);
    for (const id in redemptionRequests) {
      const r = redemptionRequests[id];
      if (r.asset_symbol === assetSymbol && !['paid', 'released', 'failed'].includes(r.status)) {
        pendingRedemption += BigInt(r.amount_minor);
      }
    }

    const reserveBalance = BigInt(reserve.confirmed_balance_minor);
    const diff = reserveBalance - (internalLiability + pendingRedemption);

    const result = {
      asset_symbol: assetSymbol,
      network: network,
      reserve_confirmed_balance_minor: reserveBalance.toString(),
      internal_liability_minor: internalLiability.toString(),
      pending_redemption_minor: pendingRedemption.toString(),
      difference_minor: diff.toString(),
      is_balanced: diff >= BigInt(0),
      reconciled_at: new Date().toISOString()
    };

    if (diff < BigInt(0)) {
      result.alert_message = `ALERT: ${assetSymbol} reserve deficit! diff=${diff} minor units (reserve=${reserveBalance} liability=${internalLiability} pending_redemption=${pendingRedemption})`;
      console.log(`[Mock] ${result.alert_message}`);
    }

    console.log(`[Mock] Reconciliation: ${assetSymbol} reserve=${reserveBalance} liability=${internalLiability} pending=${pendingRedemption} diff=${diff} balanced=${result.is_balanced}`);
    return jsonResponse(res, 200, result);
  }

  // ---- POST /api/v1/admin/reconcile ----
  // ---- POST /api/v1/admin/reconcile/daily ----
  if (method === 'POST' && (path === '/api/v1/admin/reconcile' || path === '/api/v1/admin/reconcile/daily')) {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = body ? JSON.parse(body) : {};
        const assetSymbol = data.asset_symbol || 'AGP';
        const network = data.network || 'solana-mainnet';
        const key = `${network}::${assetSymbol}`;
        const reserve = reserveAccounts[key];

        if (!reserve) {
          return jsonResponse(res, 404, { code: 404, message: `No reserve account for ${key}` });
        }

        // Internal liability
        let internalLiability = BigInt(0);
        for (const id in mintRequests) {
          const m = mintRequests[id];
          if (m.asset_symbol === assetSymbol && m.status === 'credited') {
            internalLiability += BigInt(m.amount_minor);
          }
        }

        // Pending redemption
        let pendingRedemption = BigInt(0);
        for (const id in redemptionRequests) {
          const r = redemptionRequests[id];
          if (r.asset_symbol === assetSymbol && !['paid', 'released', 'failed'].includes(r.status)) {
            pendingRedemption += BigInt(r.amount_minor);
          }
        }

        const reserveBalance = BigInt(reserve.confirmed_balance_minor);
        const diff = reserveBalance - (internalLiability + pendingRedemption);

        const result = {
          asset_symbol: assetSymbol,
          network: network,
          reserve_confirmed_balance_minor: reserveBalance.toString(),
          internal_liability_minor: internalLiability.toString(),
          pending_redemption_minor: pendingRedemption.toString(),
          difference_minor: diff.toString(),
          is_balanced: diff >= BigInt(0),
          reconciled_at: new Date().toISOString()
        };

        if (diff < BigInt(0)) {
          result.alert_message = `ALERT: ${assetSymbol} reserve deficit! diff=${diff} minor units`;
          console.log(`[Mock] ${result.alert_message}`);
        }

        console.log(`[Mock] Reconciliation triggered: ${assetSymbol} balanced=${result.is_balanced}`);
        return jsonResponse(res, 200, result);
      } catch(e) {
        return jsonResponse(res, 400, { code: 400, message: 'Reconciliation failed: ' + e.message });
      }
    });
    return;
  }

  // ---- GET /api/v1/wallet/reserve-info ----
  if (method === 'GET' && path === '/api/v1/wallet/reserve-info') {
    const network = parsedUrl.searchParams.get('network') || 'solana-mainnet';
    const assetSymbol = parsedUrl.searchParams.get('asset_symbol') || 'AGP';
    const key = `${network}::${assetSymbol}`;
    const reserve = reserveAccounts[key];
    if (!reserve) {
      return jsonResponse(res, 404, { code: 404, message: `No reserve account for ${key}` });
    }
    return jsonResponse(res, 200, {
      network: reserve.network,
      asset_symbol: reserve.asset_symbol,
      reserve_address: reserve.reserve_address,
      confirmed_balance_minor: parseInt(reserve.confirmed_balance_minor),
      pending_balance_minor: parseInt(reserve.pending_balance_minor)
    });
  }

  // ========================================================================
  // SUB-028: Multi-Chain Payment Endpoints
  // ========================================================================

  // ---- POST /api/v1/payments/create-link ----
  // Generates a Solana Pay / blockchain payment URL for the given token/amount
  if (method === 'POST' && path === '/api/v1/payments/create-link') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);

        // Validate required fields
        if (!data.token) return jsonResponse(res, 400, { code: 400, message: 'token is required (USDC, USDT, or AGP)' });
        if (!data.amount) return jsonResponse(res, 400, { code: 400, message: 'amount is required' });
        if (!data.recipient_wallet) return jsonResponse(res, 400, { code: 400, message: 'recipient_wallet is required' });

        const amountMinor = BigInt(data.amount);
        if (amountMinor <= BigInt(0)) {
          return jsonResponse(res, 400, { code: 400, message: 'amount must be positive' });
        }

        // Determine the payment rail (支持 testnet 沙箱)
        const token = data.token.toUpperCase();
        const chainRaw = (data.chain || 'solana').toLowerCase();
        const network = (data.network || 'mainnet').toLowerCase();
        // 构建多种可能的 key 顺序尝试匹配
        const candidates = [];
        if (network !== 'mainnet') {
          candidates.push(token + '-' + chainRaw + '-' + network);  // USDT-solana-devnet
          candidates.push(token + '-' + chainRaw + '-devnet');      // fallback
        }
        candidates.push(token + '-' + chainRaw);                     // USDT-solana
        if (chainRaw === 'tron') candidates.push(token + '-trc20-' + network, token + '-trc20');
        if (chainRaw === 'bsc' || chainRaw === 'bnb') candidates.push(token + '-bsc-' + network, token + '-bsc');
        if (chainRaw === 'ethereum' || chainRaw === 'eth') candidates.push(token + '-ethereum-' + network, token + '-ethereum');

        let rail = null;
        let railKey = 'unknown';
        for (const c of candidates) {
          if (PAYMENT_RAILS[c]) { rail = PAYMENT_RAILS[c]; railKey = c; break; }
        }

        if (!rail) {
          const available = Object.keys(PAYMENT_RAILS).filter(k => k.includes(token)).join(', ');
          return jsonResponse(res, 400, {
            code: 400,
            message: 'Unsupported payment rail: ' + railKey + '. Available: ' + available
          });
        }

        // Build human-readable amount (from minor units)
        const humanAmount = (Number(amountMinor) / Math.pow(10, rail.decimals)).toFixed(rail.decimals);

        // Build Solana Pay URL
        let paymentUrl = rail.payment_url_template
          .replace('{wallet}', encodeURIComponent(data.recipient_wallet))
          .replace('{amount}', humanAmount)
          .replace('{mint}', encodeURIComponent(rail.mint || ''));
        if (rail.contract) {
          paymentUrl = paymentUrl.replace('{contract}', encodeURIComponent(rail.contract));
        }

        const label = data.label || 'ANCF Payment: ' + token;
        paymentUrl += '&label=' + encodeURIComponent(label);
        if (data.memo) {
          paymentUrl += '&memo=' + encodeURIComponent(data.memo);
        }

        // Generate payment ID and store
        const paymentId = generateId('pay_');
        const expiresAt = new Date(Date.now() + 30 * 60 * 1000).toISOString(); // 30 min TTL
        const paymentRecord = {
          payment_id: paymentId,
          rail: railKey,
          token: rail.token,
          chain: rail.chain,
          network: rail.network || 'mainnet',
          amount_minor: data.amount,
          amount_human: humanAmount,
          recipient_wallet: data.recipient_wallet,
          label: label,
          memo: data.memo || null,
          payment_url: paymentUrl,
          qr_code_data: paymentUrl,
          status: 'pending',
          created_at: new Date().toISOString(),
          expires_at: expiresAt,
          tx_hash: null,
          confirmations: 0
        };
        paymentLinks[paymentId] = paymentRecord;

        console.log('[Mock Payment] Link created: ' + paymentId + ', rail=' + railKey + ', amount=' + humanAmount + ' ' + rail.token + ', chain=' + rail.chain + ', wallet=' + data.recipient_wallet.slice(0,12) + '...');
        return jsonResponse(res, 201, {
          payment_id: paymentId,
          payment_url: paymentUrl,
          qr_code_data: paymentUrl,
          expires_at: expiresAt
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid payment link request: ' + e.message });
      }
    });
    return;
  }

  // ---- GET /api/v1/payments/status ----
  // Queries payment status by payment_id
  if (method === 'GET' && path === '/api/v1/payments/status') {
    const paymentId = parsedUrl.searchParams.get('payment_id');
    if (!paymentId) {
      return jsonResponse(res, 400, { code: 400, message: 'payment_id query parameter is required' });
    }

    const payment = paymentLinks[paymentId];
    if (!payment) {
      return jsonResponse(res, 404, { code: 404, message: 'Payment not found: ' + paymentId });
    }

    // Simulate status transitions for demo purposes
    // After 15 seconds, auto-transition pending -> confirmed
    const age = Date.now() - new Date(payment.created_at).getTime();
    if (payment.status === 'pending' && age > 15000) {
      payment.status = 'confirmed';
      payment.tx_hash = generateId('tx_');
      payment.confirmations = Math.floor(Math.random() * 32) + 1;
      console.log('[Mock Payment] Auto-confirmed: ' + paymentId + ', tx=' + payment.tx_hash.slice(0,16) + '...');
    }

    // Check expiry
    if (new Date(payment.expires_at) < new Date() && payment.status === 'pending') {
      payment.status = 'expired';
    }

    return jsonResponse(res, 200, {
      payment_id: payment.payment_id,
      status: payment.status,
      rail: payment.rail,
      token: payment.token,
      chain: payment.chain,
      amount_minor: payment.amount_minor,
      amount_human: payment.amount_human,
      recipient_wallet: payment.recipient_wallet,
      payment_url: payment.payment_url,
      tx_hash: payment.tx_hash,
      confirmations: payment.confirmations,
      created_at: payment.created_at,
      expires_at: payment.expires_at
    });
  }

  // ========================================================================
  // Agent Product Upload — CRUD endpoints
  // ========================================================================

  // ---- POST /api/v1/catalog/products — Create product (SUB-026: agent ownership) ----
  if (method === 'POST' && path === '/api/v1/catalog/products') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);

        // SUB-026: Require agent authentication for product creation
        const auth = verifyAgentToken(req);
        if (!auth) {
          return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required to create products' });
        }

        // SUB-026: agent_id is derived from token — accept none from request body
        // This prevents agents from forging ownership of other agents' products
        const creatorAgentId = auth.agent_id;

        if (!data.title) return jsonResponse(res, 400, { code: 400, message: 'title is required' });
        if (!data.amount_minor) return jsonResponse(res, 400, { code: 400, message: 'amount_minor is required' });
        const crypto = require('crypto');
        const skuID = data.sku_id || 'sku_' + crypto.createHash('sha256')
          .update(creatorAgentId + ':' + data.title).digest('hex').slice(0, 16);

        if (SKU_STORE[skuID]) {
          return jsonResponse(res, 409, { code: 409, message: 'SKU ' + skuID + ' already exists' });
        }

        const priceMinor = parseInt(data.amount_minor);
        if (isNaN(priceMinor) || priceMinor < 0) {
          return jsonResponse(res, 400, { code: 400, message: 'Invalid amount_minor' });
        }

        // SUB-038: Wallet address detection — reject products with embedded wallet addresses
        const violations = sanitizeWalletFields(data);
        if (violations.length > 0) {
          return jsonResponse(res, 422, {
            code: 422,
            message: 'SECURITY: Wallet address detected in product field(s): ' + violations.join(', ') + '. Payment addresses must come from escrow system, not product data.',
            violations: violations
          });
        }

        // SUB-038: Media URL allowlist — reject products with unapproved media hosts
        if (data.media) {
          const mediaErrors = validateMediaURLs(data.media);
          if (mediaErrors.length > 0) {
            return jsonResponse(res, 422, {
              code: 422,
              message: 'SECURITY: ' + mediaErrors.join('; '),
              violations: mediaErrors
            });
          }
        }

        const now = new Date().toISOString();
        const sku = {
          sku_id: skuID,
          title: data.title,
          description: data.description || null,
          currency: data.currency || 'AGP',
          price_amount_minor: priceMinor,
          price_scale: data.scale || 6,
          stock: data.stock || 0,
          stock_hint: data.stock || 0,
          specs: data.specs || {},
          media: data.media || {},
          status: 'active',
          agent_id: creatorAgentId,       // SUB-026: ownership bound to authenticated agent
          created_at: now,
          updated_at: now
        };
        SKU_STORE[skuID] = sku;

        // SUB-026: Track product ownership
        if (!AGENT_PRODUCTS[creatorAgentId]) {
          AGENT_PRODUCTS[creatorAgentId] = [];
        }
        AGENT_PRODUCTS[creatorAgentId].push(skuID);

        console.log('[Mock] Product created: ' + skuID + ', title=' + data.title + ', agent=' + creatorAgentId);
        return jsonResponse(res, 201, sku);
      } catch (e) { return jsonResponse(res, 400, { code: 400, message: 'Invalid request: ' + e.message }); }
    });
    return;
  }

  // ---- GET /api/v1/catalog/products — List products ----
  if (method === 'GET' && path === '/api/v1/catalog/products') {
    const limit = parseInt(parsedUrl.searchParams.get('limit') || '20', 10);
    const offset = parseInt(parsedUrl.searchParams.get('offset') || '0', 10);
    const all = Object.values(SKU_STORE).sort((a, b) => b.created_at.localeCompare(a.created_at));
    const paged = all.slice(offset, offset + limit);
    return jsonResponse(res, 200, {
      items: paged,
      total: all.length,
      limit: limit,
      offset: offset
    });
  }

  // ---- GET /api/v1/catalog/products/:sku_id — Get single product ----
  if (method === 'GET') {
    const getMatch = path.match(/^\/api\/v1\/catalog\/products\/([a-zA-Z0-9_]+)$/);
    if (getMatch) {
      const sku = SKU_STORE[getMatch[1]];
      if (!sku) return jsonResponse(res, 404, { code: 404, message: 'Product not found: ' + getMatch[1] });
      return jsonResponse(res, 200, sku);
    }
  }

  // ---- PUT /api/v1/catalog/products/:sku_id — Update product (SUB-026: ownership guard) ----
  if (method === 'PUT') {
    const putMatch = path.match(/^\/api\/v1\/catalog\/products\/([a-zA-Z0-9_]+)$/);
    if (putMatch) {
      const skuID = putMatch[1];
      const sku = SKU_STORE[skuID];
      if (!sku) return jsonResponse(res, 404, { code: 404, message: 'Product not found: ' + skuID });

      // SUB-026: Verify agent ownership — only the creating agent can update
      const auth = verifyAgentToken(req);
      if (!auth) {
        return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required to update products' });
      }
      if (sku.agent_id && sku.agent_id !== 'seed' && sku.agent_id !== auth.agent_id) {
        return jsonResponse(res, 403, {
          code: 403,
          message: `Access denied: product ${skuID} is owned by agent ${sku.agent_id}, not ${auth.agent_id}`
        });
      }

      let body = '';
      req.on('data', chunk => body += chunk);
      req.on('end', () => {
        try {
          const data = JSON.parse(body);

          // SUB-038: Wallet address detection — reject updates with embedded wallet addresses
          const violations = sanitizeWalletFields(data);
          if (violations.length > 0) {
            return jsonResponse(res, 422, {
              code: 422,
              message: 'SECURITY: Wallet address detected in product field(s): ' + violations.join(', ') + '. Payment addresses must come from escrow system, not product data.',
              violations: violations
            });
          }

          // SUB-038: Media URL allowlist — reject updates with unapproved media hosts
          if (data.media) {
            const mediaErrors = validateMediaURLs(data.media);
            if (mediaErrors.length > 0) {
              return jsonResponse(res, 422, {
                code: 422,
                message: 'SECURITY: ' + mediaErrors.join('; '),
                violations: mediaErrors
              });
            }
          }

          if (data.title !== undefined) sku.title = data.title;
          if (data.description !== undefined) sku.description = data.description;
          if (data.currency !== undefined) sku.currency = data.currency;
          if (data.amount_minor !== undefined) {
            const pm = parseInt(data.amount_minor);
            if (isNaN(pm) || pm < 0) return jsonResponse(res, 400, { code: 400, message: 'Invalid amount_minor' });
            sku.price_amount_minor = pm;
          }
          if (data.scale !== undefined) sku.price_scale = data.scale;
          if (data.stock !== undefined) {
            if (data.stock < 0) return jsonResponse(res, 400, { code: 400, message: 'stock must be >= 0' });
            sku.stock = data.stock;
            sku.stock_hint = data.stock;
          }
          if (data.specs !== undefined) sku.specs = data.specs;
          if (data.media !== undefined) sku.media = data.media;
          if (data.status !== undefined) sku.status = data.status;
          sku.updated_at = new Date().toISOString();
          console.log('[Mock] Product updated: ' + skuID + ' by agent=' + auth.agent_id);
          return jsonResponse(res, 200, { sku_id: skuID, status: 'updated', message: 'Product updated successfully' });
        } catch (e) { return jsonResponse(res, 400, { code: 400, message: 'Invalid request: ' + e.message }); }
      });
      return;
    }
  }

  // ---- DELETE /api/v1/catalog/products/:sku_id — Deactivate product (SUB-026: ownership guard) ----
  if (method === 'DELETE') {
    const delMatch = path.match(/^\/api\/v1\/catalog\/products\/([a-zA-Z0-9_]+)$/);
    if (delMatch) {
      const skuID = delMatch[1];
      const sku = SKU_STORE[skuID];
      if (!sku) return jsonResponse(res, 404, { code: 404, message: 'Product not found: ' + skuID });

      // SUB-026: Verify agent ownership — only the creating agent can delete
      const auth = verifyAgentToken(req);
      if (!auth) {
        return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required to delete products' });
      }
      if (sku.agent_id && sku.agent_id !== 'seed' && sku.agent_id !== auth.agent_id) {
        return jsonResponse(res, 403, {
          code: 403,
          message: `Access denied: product ${skuID} is owned by agent ${sku.agent_id}, not ${auth.agent_id}`
        });
      }

      sku.status = 'inactive';
      sku.updated_at = new Date().toISOString();
      console.log('[Mock] Product deactivated: ' + skuID + ' by agent=' + auth.agent_id);
      return jsonResponse(res, 200, { sku_id: skuID, status: 'inactive', message: 'Product deactivated successfully' });
    }
  }

  // SUB-029: Escrow route dispatch
  if ((method === 'POST' && path === '/api/v1/escrow/lock') ||
      (method === 'POST' && path === '/api/v1/escrow/confirm-delivery') ||
      (method === 'POST' && path === '/api/v1/escrow/confirm-receipt') ||
      (method === 'POST' && path === '/api/v1/escrow/release') ||
      (method === 'GET' && path === '/api/v1/escrow/status') ||
      (method === 'GET' && path === '/api/v1/escrow/history')) {
    const escrowHandlers = require('./escrow-handlers.cjs')({
      jsonResponse: jsonResponse,
      generateId: generateId,
      autoReleaseEscrow: autoReleaseEscrow,
      tryReleaseEscrow: tryReleaseEscrow,
      ESCROW_ACCOUNTS: ESCROW_ACCOUNTS,
      ESCROW_TTL_MS: ESCROW_TTL_MS,
      AGENT_TOKENS: AGENT_TOKENS,
      AGENT_WALLETS: AGENT_WALLETS
    });
    if (path === '/api/v1/escrow/lock') return escrowHandlers.handleEscrowLock(req, res, parsedUrl, path);
    if (path === '/api/v1/escrow/confirm-delivery') return escrowHandlers.handleEscrowConfirmDelivery(req, res, parsedUrl, path);
    if (path === '/api/v1/escrow/confirm-receipt') return escrowHandlers.handleEscrowConfirmReceipt(req, res, parsedUrl, path);
    if (path === '/api/v1/escrow/release') return escrowHandlers.handleEscrowRelease(req, res, parsedUrl, path);
    if (path === '/api/v1/escrow/status') return escrowHandlers.handleEscrowStatus(req, res, parsedUrl, path);
    if (path === '/api/v1/escrow/history') return escrowHandlers.handleEscrowHistory(req, res, parsedUrl, path);
  }

  // ========================================================================
  // Dispute DAO & Sanction Committee Endpoints (SUB-030)
  // ========================================================================

  // ---- POST /api/v1/dispute/file — File a dispute ----
  if (method === 'POST' && path === '/api/v1/dispute/file') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        if (!data.order_id) return jsonResponse(res, 400, { code: 400, message: 'order_id is required' });
        if (!data.filed_by_agent_token) return jsonResponse(res, 400, { code: 400, message: 'filed_by_agent_token is required' });

        // Verify the filing agent
        const auth = verifyAgentToken(req);
        if (!auth) {
          return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required to file a dispute' });
        }

        // Check if dispute already exists for this order
        const existingDispute = Object.values(DISPUTES).find(d => d.order_id === data.order_id && d.status !== 'resolved');
        if (existingDispute) {
          return jsonResponse(res, 409, {
            code: 409,
            message: 'An active dispute already exists for this order',
            dispute_id: existingDispute.dispute_id,
            status: existingDispute.status
          });
        }

        const disputeId = generateId('dispute_');
        const now = new Date().toISOString();
        const evidenceDeadline = new Date(Date.now() + SANCTION_COMMITTEE.dispute_evidence_window_hours * 3600 * 1000).toISOString();

        const orderDetails = {
          order_id: data.order_id,
          buyer_wallet: data.buyer_wallet || 'unknown',
          seller_wallet: data.seller_wallet || 'unknown',
          total_minor: data.amount_minor || '0',
          currency: data.currency || 'AGP'
        };

        // Freeze funds in dispute escrow
        const escrowResult = freezeFundsForDispute(data.order_id, disputeId, orderDetails);

        const dispute = {
          dispute_id: disputeId,
          order_id: data.order_id,
          filed_by: auth.agent_id,
          filed_by_name: auth.agent_name,
          against: data.against || 'seller',
          reason: data.reason || 'No reason provided',
          evidence: data.evidence || null,
          status: 'open',
          votes: {},
          verdict: null,
          escrow_frozen_minor: escrowResult.frozen_minor,
          evidence_deadline: evidenceDeadline,
          voting_deadline: new Date(Date.now() + SANCTION_COMMITTEE.max_voting_period_hours * 3600 * 1000).toISOString(),
          created_at: now,
          resolved_at: null,
          audit_events: [{
            event: 'dispute_filed',
            timestamp: now,
            filed_by: auth.agent_id,
            reason: data.reason
          }]
        };

        DISPUTES[disputeId] = dispute;
        console.log('[Dispute DAO] Dispute filed: ' + disputeId.slice(0,20) + '... order=' + data.order_id.slice(0,20) + '... filed_by=' + auth.agent_id + ' funds_frozen=' + escrowResult.frozen_minor + ' AGP');

        return jsonResponse(res, 201, {
          dispute_id: disputeId,
          status: 'open',
          message: 'Dispute filed successfully. Funds frozen in escrow pending resolution.',
          evidence_deadline: dispute.evidence_deadline,
          voting_deadline: dispute.voting_deadline,
          escrow_status: escrowResult
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid dispute file request: ' + e.message });
      }
    });
    return;
  }

  // ---- POST /api/v1/dispute/vote — Sanction committee vote ----
  if (method === 'POST' && path === '/api/v1/dispute/vote') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);
        if (!data.dispute_id) return jsonResponse(res, 400, { code: 400, message: 'dispute_id is required' });
        if (!data.vote || !['approve', 'reject', 'refund'].includes(data.vote)) {
          return jsonResponse(res, 400, { code: 400, message: 'vote must be one of: approve, reject, refund' });
        }

        // Verify the voting agent
        const auth = verifyAgentToken(req);
        if (!auth) {
          return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required to vote' });
        }

        const dispute = DISPUTES[data.dispute_id];
        if (!dispute) {
          return jsonResponse(res, 404, { code: 404, message: 'Dispute not found: ' + data.dispute_id });
        }
        if (dispute.status === 'resolved') {
          return jsonResponse(res, 409, { code: 409, message: 'Dispute already resolved', verdict: dispute.verdict });
        }
        if (new Date(dispute.voting_deadline) < new Date()) {
          return jsonResponse(res, 410, { code: 410, message: 'Voting period has expired' });
        }

        // Check if agent has already voted
        if (dispute.votes && dispute.votes[auth.agent_id]) {
          return jsonResponse(res, 409, { code: 409, message: 'Agent ' + auth.agent_id + ' has already voted on this dispute' });
        }

        // Calculate voting weight based on AGP holdings
        const votingPower = calculateVotingPower(auth.agent_id);
        if (votingPower.voting_power <= 0) {
          return jsonResponse(res, 403, {
            code: 403,
            message: 'Agent does not hold AGP tokens. Voting power is zero.',
            voting_power: votingPower
          });
        }

        // Record the vote
        if (!dispute.votes) dispute.votes = {};
        dispute.votes[auth.agent_id] = {
          agent_id: auth.agent_id,
          agent_name: auth.agent_name,
          vote: data.vote,
          weight: votingPower.voting_power,
          agp_balance_minor: votingPower.agp_balance_minor,
          voted_at: new Date().toISOString(),
          comment: data.comment || null
        };

        // Add audit event
        dispute.audit_events.push({
          event: 'vote_cast',
          timestamp: new Date().toISOString(),
          agent_id: auth.agent_id,
          vote: data.vote,
          weight: votingPower.voting_power
        });

        console.log('[Dispute DAO] Vote cast: dispute=' + data.dispute_id.slice(0,20) + '... agent=' + auth.agent_id + ' vote=' + data.vote + ' weight=' + votingPower.voting_power.toFixed(4));

        // Check if threshold is met for automatic execution
        const result = executeVerdict(data.dispute_id);

        if (result.executed) {
          return jsonResponse(res, 200, {
            dispute_id: data.dispute_id,
            vote_recorded: true,
            voting_power: votingPower,
            auto_executed: true,
            verdict: result.verdict,
            total_weight: result.total_weight,
            tally: result.tally,
            message: 'Vote recorded and verdict threshold reached. Verdict automatically executed: ' + result.verdict
          });
        }

        return jsonResponse(res, 200, {
          dispute_id: data.dispute_id,
          vote_recorded: true,
          voting_power: votingPower,
          current_status: result,
          message: 'Vote recorded. Current total weight: ' + result.current_weight.toFixed(4) + ' / ' + result.required_threshold + ' required'
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid vote request: ' + e.message });
      }
    });
    return;
  }

  // ---- GET /api/v1/dispute/status — Query dispute status ----
  if (method === 'GET' && path === '/api/v1/dispute/status') {
    const disputeId = parsedUrl.searchParams.get('dispute_id');
    if (!disputeId) {
      return jsonResponse(res, 400, { code: 400, message: 'dispute_id query parameter is required' });
    }
    const dispute = DISPUTES[disputeId];
    if (!dispute) {
      return jsonResponse(res, 404, { code: 404, message: 'Dispute not found: ' + disputeId });
    }

    // Calculate current voting status
    let totalWeight = 0;
    const tally = { approve: 0, reject: 0, refund: 0 };
    const voteDetails = [];
    if (dispute.votes) {
      for (const agentId in dispute.votes) {
        const v = dispute.votes[agentId];
        totalWeight += v.weight;
        tally[v.vote] = (tally[v.vote] || 0) + v.weight;
        voteDetails.push({ agent_id: agentId, vote: v.vote, weight: v.weight, voted_at: v.voted_at });
      }
    }

    // Get escrow status
    const escrow = DISPUTE_ESCROW[dispute.order_id];

    return jsonResponse(res, 200, {
      dispute_id: disputeId,
      order_id: dispute.order_id,
      status: dispute.status,
      filed_by: dispute.filed_by,
      filed_by_name: dispute.filed_by_name,
      against: dispute.against,
      reason: dispute.reason,
      evidence: dispute.evidence,
      verdict: dispute.verdict,
      voting: {
        total_weight: totalWeight,
        required_threshold: SANCTION_COMMITTEE.threshold,
        threshold_met: totalWeight >= SANCTION_COMMITTEE.threshold,
        tally: tally,
        vote_count: voteDetails.length,
        votes: voteDetails
      },
      escrow: escrow || null,
      evidence_deadline: dispute.evidence_deadline,
      voting_deadline: dispute.voting_deadline,
      created_at: dispute.created_at,
      resolved_at: dispute.resolved_at
    });
  }

  // ---- GET /api/v1/dispute/list — List disputes involving an agent ----
  if (method === 'GET' && path === '/api/v1/dispute/list') {
    const auth = verifyAgentToken(req);
    const agentTokenParam = parsedUrl.searchParams.get('agent_token');
    let agentId = '';

    if (auth) {
      agentId = auth.agent_id;
    } else if (agentTokenParam) {
      const paramHash = crypto.createHash('sha256').update(agentTokenParam).digest('hex');
      const entry = AGENT_TOKENS[paramHash];
      if (entry) {
        agentId = entry.agent_id;
      }
    }

    const disputes = [];
    for (const id in DISPUTES) {
      const d = DISPUTES[id];
      if (d.filed_by === agentId || d.against === agentId ||
          (d.votes && d.votes[agentId])) {
        disputes.push({
          dispute_id: d.dispute_id,
          order_id: d.order_id,
          status: d.status,
          filed_by: d.filed_by,
          against: d.against,
          reason: d.reason,
          verdict: d.verdict,
          my_role: d.filed_by === agentId ? 'filer' : (d.votes && d.votes[agentId] ? 'voter' : 'counterparty'),
          created_at: d.created_at,
          resolved_at: d.resolved_at
        });
      }
    }

    return jsonResponse(res, 200, {
      agent_id: agentId,
      disputes: disputes,
      total: disputes.length
    });
  }

  // ---- POST /api/v1/dispute/join-committee — Apply to join sanction committee ----
  if (method === 'POST' && path === '/api/v1/dispute/join-committee') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      try {
        const data = JSON.parse(body);

        // Verify the applying agent
        const auth = verifyAgentToken(req);
        if (!auth) {
          return jsonResponse(res, 401, { code: 401, message: 'Valid X-ANCF-Agent-Token header is required' });
        }

        // Check if already a member
        if (AGP_BALANCES[auth.agent_id]) {
          return jsonResponse(res, 409, {
            code: 409,
            message: 'Agent ' + auth.agent_id + ' is already a committee member',
            current_role: AGP_BALANCES[auth.agent_id].role
          });
        }

        // Check AGP balance requirement
        const agpBalance = getAgentAGPBalance(auth.agent_id);
        if (BigInt(agpBalance) < BigInt(SANCTION_COMMITTEE.min_agp_to_apply_minor)) {
          return jsonResponse(res, 403, {
            code: 403,
            message: 'Insufficient AGP balance to join committee. Required: ' + SANCTION_COMMITTEE.min_agp_to_apply_minor + ', Current: ' + agpBalance,
            requirement: 'AGP > 10000'
          });
        }

        // In mock mode, auto-approve applications from agents with sufficient AGP
        AGP_BALANCES[auth.agent_id] = {
          balance_minor: agpBalance,
          role: data.requested_role || 'member',
          voting_power: Number(BigInt(agpBalance)) / Number(AGP_TOTAL_SUPPLY_MINOR),
          joined_at: new Date().toISOString()
        };

        console.log('[Dispute DAO] Committee member added: ' + auth.agent_id + ' (AGP=' + agpBalance + ', role=' + (data.requested_role || 'member') + ')');

        return jsonResponse(res, 200, {
          agent_id: auth.agent_id,
          agent_name: auth.agent_name,
          status: 'approved',
          role: AGP_BALANCES[auth.agent_id].role,
          voting_power: AGP_BALANCES[auth.agent_id].voting_power,
          agp_balance_minor: agpBalance,
          message: 'Application approved. Welcome to the sanction committee.'
        });
      } catch (e) {
        return jsonResponse(res, 400, { code: 400, message: 'Invalid join-committee request: ' + e.message });
      }
    });
    return;
  }

  // ---- GET /api/v1/dispute/committee — View sanction committee ----
  if (method === 'GET' && path === '/api/v1/dispute/committee') {
    const members = [];
    let totalVotingPower = 0;
    for (const agentId in AGP_BALANCES) {
      const m = AGP_BALANCES[agentId];
      const vp = calculateVotingPower(agentId);
      totalVotingPower += vp.voting_power;
      members.push({
        agent_id: agentId,
        role: m.role,
        agp_balance_minor: m.balance_minor,
        voting_power: vp.voting_power,
        joined_at: m.joined_at
      });
    }

    // Sort by voting power descending
    members.sort((a, b) => b.voting_power - a.voting_power);

    return jsonResponse(res, 200, {
      committee: {
        members: members,
        total_members: members.length,
        total_voting_power: totalVotingPower,
        threshold: SANCTION_COMMITTEE.threshold,
        threshold_description: '51% voting weight required to execute verdict',
        expansion_rule: SANCTION_COMMITTEE.expansion_rule,
        min_agp_to_apply: '10000 AGP'
      },
      agp_total_supply_minor: AGP_TOTAL_SUPPLY_MINOR.toString(),
      active_disputes: Object.values(DISPUTES).filter(d => d.status !== 'resolved').length,
      resolved_disputes: Object.values(DISPUTES).filter(d => d.status === 'resolved').length
    });
  }


  // ---- 404 ----
  jsonResponse(res, 404, { code: 404, message: `Not found: ${method} ${path}` });
});

// ===== Persistence: restore + auto-save =====
function getStateFn() { return { SKU_STORE, quotes, intents, AGENT_TOKENS, AGENT_PRODUCTS, AGENT_WALLETS, DEPOSIT_INTENTS: depositIntents, MINT_REQUESTS: mintRequests, REDEMPTION_REQUESTS: redemptionRequests, ESCROW_ACCOUNTS, transactionRegistry, DISPUTES, AGP_BALANCES, DISPUTE_ESCROW, paymentLinks }; }

// Restore other in-memory stores
if (persistedState.quotes) Object.assign(quotes, persistedState.quotes);
if (persistedState.intents) Object.assign(intents, persistedState.intents);
if (persistedState.AGENT_TOKENS) Object.assign(AGENT_TOKENS, persistedState.AGENT_TOKENS);
if (persistedState.AGENT_PRODUCTS) Object.assign(AGENT_PRODUCTS, persistedState.AGENT_PRODUCTS);
if (persistedState.AGENT_WALLETS) Object.assign(AGENT_WALLETS, persistedState.AGENT_WALLETS);
if (persistedState.DEPOSIT_INTENTS) Object.assign(depositIntents, persistedState.DEPOSIT_INTENTS);
if (persistedState.MINT_REQUESTS) Object.assign(mintRequests, persistedState.MINT_REQUESTS);
if (persistedState.REDEMPTION_REQUESTS) Object.assign(redemptionRequests, persistedState.REDEMPTION_REQUESTS);
if (persistedState.ESCROW_ACCOUNTS) Object.assign(ESCROW_ACCOUNTS, persistedState.ESCROW_ACCOUNTS);
if (persistedState.transactionRegistry) transactionRegistry.push(...persistedState.transactionRegistry);
if (persistedState.DISPUTES) Object.assign(DISPUTES, persistedState.DISPUTES);
if (persistedState.AGP_BALANCES) Object.assign(AGP_BALANCES, persistedState.AGP_BALANCES);
if (persistedState.paymentLinks) Object.assign(paymentLinks, persistedState.paymentLinks);

// ===== End persistence =====

server.listen(PORT, '127.0.0.1', () => {
  console.log('');
  console.log('==============================================');
  console.log('  ANCF Mock API Server');
  console.log('  Running at: http://127.0.0.1:' + PORT);
  console.log('==============================================');
  console.log('  Phase 1-2 Endpoints (Commerce):');
  console.log('    GET  /.well-known/agent-rules.json');
  console.log('    GET  /health');
  console.log('    GET  /api/v1/cli/search?q=H100');
  console.log('    POST /api/v1/cli/quote');
  console.log('    POST /api/v1/cli/checkout/prepare');
  console.log('    POST /api/v1/cli/checkout/commit');
  console.log('==============================================');
  console.log('  Phase 3 Endpoints (AgentPay AGP + Reconciliation):');
  console.log('    POST /api/v1/wallet/deposit-intents');
  console.log('    POST /api/v1/wallet/deposit-confirm');
  console.log('    POST /api/v1/wallet/redeem');
  console.log('    POST /api/v1/wallet/redeem/:id/process');
  console.log('    POST /api/v1/wallet/redeem/:id/payout');
  console.log('    POST /api/v1/wallet/redeem/:id/release');
  console.log('    GET  /api/v1/wallet/mint-status');
  console.log('    GET  /api/v1/wallet/redeem-status');
  console.log('    GET  /api/v1/wallet/reserve-info');
  console.log('    GET  /api/v1/admin/reconciliation');
  console.log('    POST /api/v1/admin/reconcile');
  console.log('    POST /api/v1/admin/reconcile/daily');
  console.log('==============================================');
  console.log('  SUB-029 Escrow Endpoints (Custodial + Auto-Release):');
  console.log('    POST /api/v1/escrow/lock');
  console.log('    POST /api/v1/escrow/confirm-delivery');
  console.log('    POST /api/v1/escrow/confirm-receipt');
  console.log('    POST /api/v1/escrow/release');
  console.log('    GET  /api/v1/escrow/status?order_id=xxx');
  console.log('    GET  /api/v1/escrow/history?agent_token=xxx');
  console.log('==============================================');
  console.log('  Agent Auth Endpoints (SUB-026):');
  console.log('    POST /api/v1/auth/register-agent');
  console.log('    POST /api/v1/auth/bind-wallet');
  console.log('    GET  /api/v1/auth/agent-info');
  console.log('    (all catalog mutations require X-ANCF-Agent-Token)');
  console.log('==============================================');
  console.log('  Agent Product Upload Endpoints:');
  console.log('    POST /api/v1/catalog/products');
  console.log('    GET  /api/v1/catalog/products');
  console.log('    GET  /api/v1/catalog/products/:sku_id');
  console.log('    PUT  /api/v1/catalog/products/:sku_id');
  console.log('    DELETE /api/v1/catalog/products/:sku_id');
  console.log('==============================================');
  console.log('');
  console.log('==============================================');
  console.log('  Dispute DAO & Sanction Committee (SUB-030):');
  console.log('    POST /api/v1/dispute/file');
  console.log('    POST /api/v1/dispute/vote');
  console.log('    GET  /api/v1/dispute/status?dispute_id=xxx');
  console.log('    GET  /api/v1/dispute/list?agent_token=xxx');
  console.log('    POST /api/v1/dispute/join-committee');
  console.log('    GET  /api/v1/dispute/committee');
  console.log('==============================================');
  console.log('  SUB-028 Multi-Chain Payments:');
  console.log('    POST /api/v1/payments/create-link');
  console.log('    GET  /api/v1/payments/status?payment_id=xxx');
  console.log('    Supported rails: USDC-solana, USDT-solana,');
  console.log('    USDC-ethereum, AGP-solana');
  console.log('==============================================');
  console.log('');
  console.log('Seed data: 3 GPU SKUs (H100, A100, L40S) + 2 Reserve Accounts (solana-mainnet, sonic-l2)');
  console.log('');
});

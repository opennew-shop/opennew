/**
 * Token Security Unit Tests — fix/token-id-security
 * Verifies: unique tokens, hash storage, no plaintext leak, anti-spoofing
 */
const http = require('http');
const crypto = require('crypto');
const { execSync } = require('child_process');

const API = process.env.API_BASE || 'http://127.0.0.1:8080';
let pass = 0, fail = 0;
function ok(m) { pass++; console.log('  ✅', m); }
function no(m) { fail++; console.log('  ❌', m); }

function req(method, path, body, headers = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(API + path);
    const r = http.request({ hostname: u.hostname, port: u.port, path: u.pathname + u.search, method, headers: { 'Content-Type': 'application/json', ...headers } }, res => {
      let d = ''; res.on('data', c => d += c);
      res.on('end', () => { try { resolve({ status: res.statusCode, body: JSON.parse(d || '{}') }); } catch { resolve({ status: res.statusCode, body: d }); } });
    });
    r.on('error', reject);
    if (body) r.write(JSON.stringify(body));
    r.end();
  });
}

(async () => {
  console.log('=== Token Security Tests ===\n');

  // 1. Register two agents → tokens must differ
  const a1 = await req('POST', '/api/v1/auth/register-agent', { agent_name: 'TokTest1', agent_type: 'merchant' });
  const a2 = await req('POST', '/api/v1/auth/register-agent', { agent_name: 'TokTest2', agent_type: 'merchant' });
  const t1 = a1.body.token, t2 = a2.body.token;

  console.log('1. Token uniqueness');
  (t1 && t2 && t1 !== t2) ? ok(`两 token 不同: ${t1.slice(0,18)}... vs ${t2.slice(0,18)}...`) : no(`token 重复或为空! t1=${t1} t2=${t2}`);

  console.log('2. Token randomness (length & entropy)');
  const hexPart = (t1 || '').replace('ancf_agent_', '');
  (hexPart.length === 64) ? ok(`64 hex chars (256-bit): ${hexPart.length}`) : no(`token 随机部分长度异常: ${hexPart.length} (期望64)`);
  /^[0-9a-f]{64}$/.test(hexPart) ? ok('纯 hex 格式') : no(`格式异常: ${hexPart.slice(0,20)}`);

  console.log('3. Valid token works');
  const bind = await req('POST', '/api/v1/auth/bind-wallet', { wallet_address: 'TEST_W', chain: 'solana' }, { 'X-ANCF-Agent-Token': t1 });
  (bind.status === 200) ? ok('有效 token 绑定钱包成功') : no(`有效 token 被拒: HTTP ${bind.status}`);

  console.log('4. Empty/fake token rejected (anti-spoof)');
  const empty = await req('POST', '/api/v1/catalog/products', { title: 'x', amount_minor: '1' }, { 'X-ANCF-Agent-Token': 'ancf_agent_' });
  (empty.status === 401) ? ok('空随机 token 被拒 (401)') : no(`空 token 冒充成功! HTTP ${empty.status} — 严重漏洞`);

  const fake = await req('POST', '/api/v1/catalog/products', { title: 'x', amount_minor: '1' }, { 'X-ANCF-Agent-Token': 'ancf_agent_' + 'f'.repeat(64) });
  (fake.status === 401) ? ok('伪造 token 被拒 (401)') : no(`伪造 token 冒充成功! HTTP ${fake.status}`);

  console.log('5. No plaintext token in persisted state');
  try {
    const state = execSync('cat /opt/ancf/.mock-state.json 2>/dev/null || echo "{}"').toString();
    const hasPlaintext = state.includes(t1) || state.includes(t2);
    !hasPlaintext ? ok('持久化文件中无明文 token (仅 SHA-256 哈希)') : no('明文 token 落盘! 文件泄露=身份泄露');
  } catch (e) { console.log('  ⚠ 跳过 (无法读 state 文件):', e.message.slice(0, 40)); }

  console.log('6. Token stored as hash key');
  try {
    const state = JSON.parse(execSync('cat /opt/ancf/.mock-state.json 2>/dev/null || echo "{}"').toString());
    const keys = Object.keys(state.AGENT_TOKENS || {});
    const allHashes = keys.every(k => /^[0-9a-f]{64}$/.test(k));
    (keys.length > 0 && allHashes) ? ok(`所有 token key 都是 SHA-256 哈希 (${keys.length} 条)`) : (keys.length === 0 ? console.log('  ⚠ 无 token 记录') : no('存在非哈希 key'));
  } catch (e) { console.log('  ⚠ 跳过'); }

  console.log(`\n=== ${pass} 通过 / ${fail} 失败 ===`);
  process.exit(fail > 0 ? 1 : 0);
})();

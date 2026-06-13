# ANCF Zero-Frontend Commerce — Frontend Design Spec

> 面向 Gemini / AI Agent 的前端组件生成规范
> 版本: 1.0 | 协议: ANCF-1.0 | 日期: 2026-06-04

---

## 1. 架构概述

ANCF 不是传统电商前端，而是由 **Agent 发现 → 校验 → 本地渲染 → 提交意图** 的商业协议。前端只有两层：

```
┌─────────────────────────────────────────────┐
│  Node Agent (agents/node-local-renderer)     │
│  127.0.0.1:3000                              │
│  ├─ Manifest 获取/校验                        │
│  ├─ CSP 安全头注入                            │
│  ├─ HTML 页面生成                             │
│  └─ Agent Bridge API (/bridge)               │
├─────────────────────────────────────────────┤
│  Web Components (firmware/components)        │
│  无框架，原生 Custom Elements v1 + Shadow DOM │
│  ├─ <ancf-theme>     主题注入                 │
│  ├─ <ancf-search>    商品搜索                 │
│  ├─ <ancf-quote>     报价展示                 │
│  └─ <ancf-checkout>  结算确认                 │
├─────────────────────────────────────────────┤
│  Agent Bridge (agent-bridge.ts)              │
│  白名单命令中转，浏览器不直接 fetch 后端       │
└─────────────────────────────────────────────┘
```

**核心原则：本地 UI 只是临时交互面，不可信。价格/库存/交易状态以后端确认为准。**

---

## 2. 设计令牌 (Design Tokens)

`<ancf-theme>` 注入以下 CSS Custom Properties 到 `:root`：

| Token 名 | CSS 变量 | 默认值 | 用途 |
|----------|----------|--------|------|
| primary | `--ancf-primary` | `#00FFA3` | 主色调（按钮、价格、高亮） |
| background | `--ancf-background` | `#0D0E12` | 页面背景 |
| text | `--ancf-text` | `#FFFFFF` | 主文字色 |
| surface | `--ancf-surface` | `#1A1A2E` | 卡片/面板背景 |
| border | `--ancf-border` | `#2A2A4A` | 边框色 |
| radius | `--ancf-radius` | `8px` | 圆角 |
| font | `--ancf-font` | `'Inter', system-ui, sans-serif` | 字体栈 |
| danger | `--ancf-danger` | `#FF4444` | 危险/错误色 |
| warning | `--ancf-warning` | `#FFAA00` | 警告色 |
| success | `--ancf-success` | `#00FFA3` | 成功色 |

**注入方式**：优先 `document.adoptedStyleSheets`，回退 `<style>` 注入 `<head>`。

**安全**：token 值只设置 CSS 变量，不执行 JS，不加载外部资源。token JSON 解析后对值做 `[<>"'&;{}()]` 字符过滤。

---

## 3. 组件规范

### 3.1 `<ancf-theme>` — 主题注入

**职责**：无可见渲染，只注入 CSS 变量到 `:root`。

**属性**：

| 属性 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `tokens` | JSON string | 否 | 覆盖默认 token，如 `'{"primary":"#FF6B35"}'` |

**事件**：无

**用法**：
```html
<ancf-theme tokens='{"primary":"#00FFA3","background":"#0D0E12","text":"#FFFFFF"}'></ancf-theme>
```

**实现要点**：
- `observedAttributes = ['tokens']`
- `connectedCallback` 时注入，`disconnectedCallback` 时移除
- 支持动态更新（`attributeChangedCallback` 重新注入）
- 默认 token 硬编码在静态属性 `DEFAULTS` 中

---

### 3.2 `<ancf-search>` — 商品搜索

**职责**：搜索框 + 结果列表，用户选择商品后触发 `ancf:select` 事件。

**属性**：

| 属性 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `api-base` | string | 否 | `http://127.0.0.1:8080` | API 网关地址（直连 fallback 时用） |

**事件**：

| 事件名 | 方向 | detail | 触发时机 |
|--------|------|--------|----------|
| `ancf:select` | 组件→页面 | `{ sku_id, title, price, stock_hint, specs, media }` | 用户点击/回车选择结果 |
| `ancf:search-start` | 组件→页面 | `{ query }` | 搜索开始 |
| `ancf:search-end` | 组件→页面 | `{ query, itemCount, error? }` | 搜索结束 |

**内部状态**：

```
loading: boolean        // 搜索中
results: SearchResultItem[]  // 结果列表
error: string | null    // 错误信息
selectedIndex: number   // 键盘选中索引 (-1 = 无)
query: string           // 当前搜索词
```

**SearchResultItem 接口**：
```typescript
interface SearchResultItem {
    sku_id: string;
    title: string;
    price: {
        currency: string;      // "vUSDC"
        amount_minor: string;  // "2450000" (6 decimals = 2.45)
        scale: number;         // 6
    };
    stock_hint?: number;
    specs?: Record<string, string>;
    media?: { thumbnail?: string };
}
```

**UI 状态机**：

```
初始状态 (无输入)
  │
  ├─ 用户输入 → 300ms debounce → loading 状态 (spinner)
  │     │
  │     ├─ 成功 → 结果列表 (ul/li)
  │     │         ├─ 键盘↑↓ 导航
  │     │         ├─ Enter 选择 → dispatch ancf:select
  │     │         └─ Escape 清空
  │     │
  │     └─ 失败 → error 状态 (错误信息 + Retry 按钮)
  │
  └─ 空结果 → empty 状态 ("No products found")
```

**搜索请求方式**（优先级）：
1. `window.__ancfBridge.handleCommand({ command: 'ancf:search', params: { query, limit }, requestId })` — Agent Bridge
2. `fetch(apiBase + '/api/v1/cli/search?q=' + query)` — 直连 fallback

**安全要点**：
- 所有来自搜索结果的文本（title/sku_id/specs）必须 `escapeHtml()` 处理 `& < > " '`
- 图片 URL 必须 `escapeAttr()` 处理引号
- 商品描述中的文本不执行任何脚本

**价格格式化**：
```typescript
formatPrice(price): string {
    const major = parseInt(price.amount_minor) / Math.pow(10, price.scale);
    return `${major.toFixed(price.scale)} ${price.currency}`;
}
// "2450000" + scale=6 → "2.450000 vUSDC"
```

---

### 3.3 `<ancf-quote>` — 报价展示

**职责**：展示报价明细表格 + 过期倒计时 + 确认按钮。

**属性**：

| 属性 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `quote-data` | JSON string | 是 | 报价响应 JSON（来自 `POST /api/v1/cli/quote`） |

**事件**：

| 事件名 | 方向 | detail | 触发时机 |
|--------|------|--------|----------|
| `ancf:confirm` | 组件→页面 | `{ quote_id }` | 用户点击 Confirm Quote |
| `ancf:quote-expired` | 组件→页面 | `{ quote_id }` | 倒计时归零 |

**QuoteResponse 接口**：
```typescript
interface QuoteResponse {
    quote_id: string;
    currency: string;        // "vUSDC"
    total_minor: string;     // "4900000"
    scale: number;           // 6
    expires_at: string;      // ISO 8601
    lines: QuoteLine[];
}
interface QuoteLine {
    sku_id: string;
    quantity: number;
    unit_price_minor: string;
    line_total_minor: string;
}
```

**UI 结构**：
```
┌─────────────────────────────────────────┐
│ Quote: quote_xxx...    Expires in: 4m 32s│ ← header
├─────────────────────────────────────────┤
│ SKU              Qty  Unit Price  Total  │ ← table
│ sku_gpu_h100_v1   2   2.45 vUSDC  4.90   │
├─────────────────────────────────────────┤
│ TOTAL           4.90 vUSDC   [Confirm]   │ ← footer
└─────────────────────────────────────────┘
```

**状态**：
- `quote = null` → 空状态："No quote data available"
- `quote 有效` → 表格 + 倒计时 + 确认按钮可用
- `expired = true` → 表格 + "EXPIRED" 徽章 + 按钮 disabled，dispatch `ancf:quote-expired`

**倒计时逻辑**：
- 每秒 `setInterval` 更新 `timeRemaining`
- 倒计时归零 → 设置 `expired = true` → dispatch `ancf:quote-expired`
- `disconnectedCallback` 时必须 `clearInterval`

**价格格式化**：同 `<ancf-search>` 的 `formatPrice` 逻辑。

---

### 3.4 `<ancf-checkout>` — 结算确认

**职责**：展示最终订单摘要，用户确认后触发 checkout commit。

**属性**：

| 属性 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `order-intent-data` | JSON string | 是 | checkout/prepare 响应 |
| `quote-data` | JSON string | 否 | 报价响应（展示 line items 用） |
| `wallet-address` | string | 否 | 钱包地址（展示用） |
| `shop-domain` | string | 否 | 商户域名 |
| `shop-id` | string | 否 | 商户 ID |

**事件**：

| 事件名 | 方向 | detail | 触发时机 |
|--------|------|--------|----------|
| `ancf:checkout-confirm` | 组件→页面 | `{ order_intent_id, quote_id, wallet }` | 用户点击 Confirm Checkout |

**OrderIntentResponse 接口**：
```typescript
interface OrderIntentResponse {
    order_intent_id: string;
    quote_id: string;
    signable_payload: {
        domain: string;       // "yourshop.com"
        shop_id: string;      // "zero_shop_sol_01"
        network: string;      // "solana-mainnet"
        wallet: string;
        quote_id: string;
        total_minor: string;
        currency: string;
        expires_at: string;
        nonce: string;        // 128-bit random hex
    };
}
```

**UI 结构**：
```
┌─────────────────────────────────────────┐
│ ⚠ 安全警告：本地临时界面，不可信          │ ← 黄色警告条（始终显示）
├─────────────────────────────────────────┤
│ Domain: yourshop.com                     │ ← 商户信息
│ Shop: zero_shop_sol_01                   │
│ Network: solana-mainnet                  │
├─────────────────────────────────────────┤
│ Wallet Address                           │ ← 钱包
│ DEMO_WALLET_ABC123...                    │
├─────────────────────────────────────────┤
│ Order Intent: intent_xxx...              │ ← 意图 ID
├─────────────────────────────────────────┤
│ SKU              Qty  Unit Price  Total  │ ← SKU 表格
│ sku_gpu_h100_v1   2   2.45 vUSDC  4.90   │
├─────────────────────────────────────────┤
│ TOTAL (Backend Authoritative)            │ ← 总价 + 确认按钮
│ 4.90 vUSDC           [Confirm Checkout]  │
└─────────────────────────────────────────┘
```

**状态**：
- `orderIntent = null` → 空状态："No order intent available"
- `confirmed = false` → 正常展示，按钮可用
- `confirmed = true` → 按钮 disabled，显示 "Confirmed"

**安全要点**：
- 警告条 **始终显示**，不可被用户关闭
- 总价标签必须注明 "Backend Authoritative"
- 所有外部数据 HTML-escaped
- 组件不发起任何网络请求（由页面 JS 通过 Agent Bridge 处理）

---

## 4. Agent Bridge 协议

### 4.1 架构

```
浏览器 Web Component          Node Agent 进程           后端 API Gateway
─────────────────────    ─────────────────────    ─────────────────────
                          agent-bridge.ts
ancf-search ──┐           (window.__ancfBridge)     127.0.0.1:8080
ancf-quote  ──┼─ handleCommand() ──→ /bridge ──→   Go API Gateway
ancf-checkout──┘           (白名单校验 + 代理)
```

### 4.2 Bridge API 端点

**POST /bridge**（Agent 进程内）

请求体：
```json
{
    "command": "ancf:search",
    "params": { "query": "H100", "limit": 20 },
    "requestId": "uuid"
}
```

响应体：
```json
{
    "requestId": "uuid",
    "result": { ... },
    "error": "错误信息（仅失败时）"
}
```

### 4.3 白名单命令

| 命令 | 参数 | 对应后端 API | 说明 |
|------|------|-------------|------|
| `ancf:search` | `{ query, limit }` | `GET /api/v1/cli/search` | 商品搜索 |
| `ancf:quote` | `{ wallet, network, lines[] }` | `POST /api/v1/cli/quote` | 请求报价 |
| `ancf:checkout_prepare` | `{ quote_id, wallet, network, agent_session_id }` | `POST /api/v1/cli/checkout/prepare` | 生成订单意图 |
| `ancf:checkout_commit` | `{ order_intent_id, quote_id, wallet, wallet_signature, agent_session_id, idempotency_key }` | `POST /api/v1/cli/checkout/commit` | 提交结算 |
| `ancf:ready` | `{}` | 无 | 健康检查 |

**非白名单命令一律返回 403**。

### 4.4 Agent Bridge 客户端接口

```typescript
// window.__ancfBridge 全局暴露
interface AgentBridge {
    handleCommand(cmd: {
        command: string;
        params: Record<string, unknown>;
        requestId: string;
    }): Promise<{ requestId: string; result?: unknown; error?: string }>;
}
```

### 4.5 超时与错误处理

- 所有 HTTP 请求 15 秒超时（AbortController）
- 网络错误 → 返回 `{ requestId, error: "..." }`
- HTTP 非 2xx → 返回 `{ requestId, error: "API returned HTTP xxx" }`
- 响应格式校验失败 → 返回 `{ requestId, error: "Response missing xxx" }`

---

## 5. 页面布局与交互流程

### 5.1 HTML 页面结构

Agent 生成的 checkout 页面 (`/` 路由) 结构：

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ANCF Checkout — {shop_id}</title>
    <!-- 内联基础样式 -->
</head>
<body>
    <ancf-theme tokens='{...}'></ancf-theme>

    <div class="container">
        <!-- Header: domain, shop_id, protocol_version -->
        <!-- Security Warning: 黄色警告条 (始终显示) -->
        <!-- Policy Info: 策略状态展示 (Autonomous Checkout, Human Confirmation) -->

        <!-- Section 1: Search -->
        <ancf-search id="search-component" api-base="..."></ancf-search>

        <!-- Section 2: Quote (初始隐藏) -->
        <ancf-quote id="quote-component"></ancf-quote>

        <!-- Section 3: Checkout (初始隐藏) -->
        <ancf-checkout id="checkout-component"></ancf-checkout>

        <!-- Footer -->
    </div>

    <!-- 固件组件 -->
    <script type="module" src="/firmware/ancf-theme.js"></script>
    <script type="module" src="/firmware/ancf-search.js"></script>
    <script type="module" src="/firmware/ancf-quote.js"></script>
    <script type="module" src="/firmware/ancf-checkout.js"></script>
    <script type="module" src="/firmware/agent-bridge.js"></script>

    <!-- 页面交互编排 -->
    <script type="module">
        // 事件流编排:
        // ancf:select → 调 quote API → 显示 quote section
        // ancf:confirm → 调 checkout_prepare API → 显示 checkout section
        // ancf:checkout-confirm → 提示签名 → 调 checkout_commit API → 显示结果
    </script>
</body>
</html>
```

### 5.2 完整交互流程

```
用户输入搜索词
  │
  ▼
<ancf-search> search "H100"
  │─ dispatch ancf:search-start
  │─ POST /bridge { command: "ancf:search", params: { query: "H100" } }
  │─ 渲染结果列表 (thumbnail + title + sku_id + price + stock)
  │─ dispatch ancf:search-end
  │
  ▼ 用户点击结果
<ancf-search> dispatch ancf:select { sku_id, title, price, ... }
  │
  ▼ 页面编排 JS 监听到 ancf:select
1. prompt 钱包地址
2. POST /bridge { command: "ancf:quote", params: { wallet, lines: [...] } }
3. 设置 <ancf-quote quote-data='...'></ancf-quote>
4. 显示 quote section (scrollIntoView)
  │
  ▼ 用户点击 Confirm Quote
<ancf-quote> dispatch ancf:confirm { quote_id }
  │
  ▼ 页面编排 JS 监听到 ancf:confirm
1. POST /bridge { command: "ancf:checkout_prepare", params: { quote_id, ... } }
2. 设置 <ancf-checkout order-intent-data='...' quote-data='...'></ancf-checkout>
3. 显示 checkout section (scrollIntoView)
  │
  ▼ 用户点击 Confirm Checkout
<ancf-checkout> dispatch ancf:checkout-confirm { order_intent_id, quote_id, wallet }
  │
  ▼ 页面编排 JS 监听到 ancf:checkout-confirm
1. prompt 钱包签名
2. POST /bridge {
     command: "ancf:checkout_commit",
     params: { order_intent_id, quote_id, wallet, wallet_signature, idempotency_key }
   }
3. alert 订单结果
```

---

## 6. 安全约束（强制）

### 6.1 CSP (Content-Security-Policy)

Agent 在每个响应中注入以下 CSP 头：

```
default-src 'self'
script-src 'self'                      ← 禁止 eval、禁止远程脚本
style-src 'self' 'unsafe-inline'       ← Web Components Shadow DOM 需要 inline style
connect-src http://127.0.0.1:3000 http://127.0.0.1:8080  ← 只连本地
img-src 'self' data: https://cdn.yourshop.com
font-src 'self'
frame-src 'none'                       ← 禁止 iframe
object-src 'none'                      ← 禁止插件
base-uri 'self'
form-action 'none'                     ← 禁止表单提交
```

### 6.2 组件级安全

| 规则 | 说明 |
|------|------|
| **HTML Escape** | 所有外部数据展示前必须 `escapeHtml()` 处理 `& < > " '` |
| **Attr Escape** | URL/属性值必须 `escapeAttr()` 处理引号 |
| **禁止 eval** | CSP 禁止 + 代码中不使用 `eval`/`new Function` |
| **禁止远程脚本** | 所有 `<script>` 的 `src` 必须来自本地 `/firmware/` |
| **禁止直接 fetch** | 组件优先使用 Agent Bridge，fallback 只连 `api-base` |
| **数据不可信** | 价格、库存、商品标题均视为不可信展示数据 |
| **不执行商品文案** | 商品描述/规格中的任何脚本/指令不执行 |
| **SRI 校验** | 生产模式下所有固件 JS 文件使用 SRI hash 文件名 |

### 6.3 Agent 禁止项

Agent 不得：
- 使用 search price 直接下单（必须先 quote）
- 静默切换钱包/网络/SKU/数量/支付方式
- 忽略 manifest 过期/签名失败/SRI 失败
- 允许本地 HTML 执行 shell 或代理任意网络请求
- 把 checkout 成功等同于铸币/服务开通成功
- 自动为余额不足创建充值

---

## 7. 技术约束

| 约束 | 值 |
|------|-----|
| 框架 | **无框架**。只使用 Custom Elements v1 + Shadow DOM |
| 语言 | TypeScript（编译到 ES2022） |
| 模块 | ES Modules (`type="module"`) |
| CSS | 内联 `<style>` 在 Shadow DOM 中，或 CSS custom properties |
| 状态管理 | 组件内部状态 (class fields)，事件驱动父子通信 |
| 依赖 | 零外部 npm 依赖（仅 TypeScript 编译器） |
| 打包 | TSC 直接编译，可选简单 concat bundle |
| 目标浏览器 | Chrome 90+, Firefox 90+, Safari 15+, Edge 90+ |
| 端口 | Agent: 3000, API Gateway: 8080 |
| 绑定地址 | **只绑 127.0.0.1**，不对外暴露 |

---

## 8. 文件输出约定

```
firmware/components/
  src/
    ancf-theme.ts       ← 主题注入组件
    ancf-search.ts      ← 搜索组件
    ancf-quote.ts       ← 报价组件
    ancf-checkout.ts    ← 结算组件
    agent-bridge.ts     ← Agent Bridge (浏览器端)
  dist/
    ancf-theme.{hash}.js   ← SRI hash 文件名
    ancf-search.{hash}.js
    ancf-quote.{hash}.js
    ancf-checkout.{hash}.js
    agent-bridge.{hash}.js
    ancf-theme.js          ← 非 hash 别名（开发用）
    ancf-search.js
    ancf-quote.js
    ancf-checkout.js
    agent-bridge.js
    manifest.json          ← 固件清单 (url → integrity 映射)
```

**SRI 生成**：`sha384-` 格式，通过 `openssl dgst -sha384 -binary | base64` 或 Node.js `crypto.createHash('sha384')` 计算。

---

## 9. 测试端点

开发时使用 Mock API (Node.js, `test-mock-server.cjs`) 提供以下端点：

| 方法 | 路径 | 返回 |
|------|------|------|
| GET | `/health` | `{ status: "ok" }` |
| GET | `/.well-known/agent-rules.json` | 完整 ANCF manifest |
| GET | `/api/v1/cli/search?q=&limit=` | 种子 SKU 搜索结果 |
| POST | `/api/v1/cli/quote` | 报价响应 |
| POST | `/api/v1/cli/checkout/prepare` | signable payload |
| POST | `/api/v1/cli/checkout/commit` | 订单确认 |

种子数据（3 个 GPU SKU）：

| sku_id | title | price | stock_hint |
|--------|-------|-------|------------|
| `sku_gpu_h100_v1` | H100 Compute Rental, Hourly | 2.45 vUSDC | 42 |
| `sku_gpu_a100_v1` | A100 Compute Rental, Hourly | 1.20 vUSDC | 128 |
| `sku_gpu_l40s_v1` | L40S Compute Rental, Hourly | 0.65 vUSDC | 256 |

---

## 10. Gemini 实现检查清单

生成前端组件时逐项验证：

- [ ] 4 个 Custom Elements 全部使用 Shadow DOM (`attachShadow({ mode: 'open' })`)
- [ ] 所有外部数据 HTML-escaped（`escapeHtml` 函数处理 `& < > " '`）
- [ ] `<ancf-theme>` 使用 `adoptedStyleSheets` 优先，`<style>` fallback
- [ ] `<ancf-theme>` token 值经过 `[<>"'&;{}()]` 过滤
- [ ] `<ancf-search>` 300ms debounce，键盘 ↑↓ Enter Escape 导航
- [ ] `<ancf-search>` 三态 UI：loading spinner / error+retry / empty
- [ ] `<ancf-search>` 优先用 `window.__ancfBridge`，不存在时直连 `api-base`
- [ ] `<ancf-quote>` 每秒倒计时，归零触发 `ancf:quote-expired` 事件
- [ ] `<ancf-quote>` 过期后确认按钮 disabled
- [ ] `<ancf-checkout>` 黄色警告条始终显示
- [ ] `<ancf-checkout>` 总价标签注明 "Backend Authoritative"
- [ ] `<ancf-checkout>` 确认后按钮 disabled，不重复提交
- [ ] `agent-bridge.ts` 白名单命令集合，非白名单返回 error
- [ ] `agent-bridge.ts` 15 秒 AbortController 超时
- [ ] `agent-bridge.ts` 自动检测浏览器环境并 `exposeToBrowser()`
- [ ] 所有组件在 `disconnectedCallback` 中清理 timer/listener
- [ ] `observedAttributes` 正确声明，`attributeChangedCallback` 触发重新渲染
- [ ] 价格格式化：`parseInt(amount_minor) / 10^scale`，显示到 scale 位小数
- [ ] CSS 变量名统一 `--ancf-` 前缀
- [ ] 组件间通过 CustomEvent 通信，不直接引用对方的 DOM

# ANCF / Zero-Frontend Commerce 主进度文档

> 更新时间：2026-06-12  
> 作用：统一合并 `claude进度.md` 与 `目前进度ccodex.md`。  
> 阅读建议：先看“已交叉验证结论”，再看后面的“详细阶段记录”和“前端/Stitch 过程记录”。

---

## 1. 已交叉验证结论

这部分是我按当前仓库实际文件、目录和代码痕迹重新核对后的结论，优先级最高。

### 1.1 总体判断

- Phase 1-5 的主体代码、模板、测试资产已经落库，项目不是纯文档方案。
- `AGP` 更名已经部分落地，不再只是规划，至少已进入 schema、部分服务代码、mock、前端显示与链上脚本。
- Phase 6 不是“完全未开始”，但也不能写成“正式完整交付”；更准确的说法是：**部分原型已在 mock / 脚本 / onchain 项目中出现，正式服务化尚未收口**。

### 1.2 已确认存在的服务代码

仓库内已确认存在实际代码文件的服务目录：

- `services/api-gateway/`
- `services/catalog/`
- `services/ledger/`
- `services/mint/`
- `services/chain-adapter/`
- `services/audit/`
- `services/provisioning/`
- `services/quote/`
- `services/checkout/`

已确认存在 `cmd/main.go` 的独立服务：

- `api-gateway`
- `catalog`
- `ledger`
- `mint`
- `chain-adapter`
- `audit`
- `provisioning`

需要保守表述的点：

- `quote/` 和 `checkout/` 目前能看到完整 `internal/` 代码，但未看到独立 `cmd/main.go`。
- `services/payment/` 当前没有实际服务代码文件。
- `services/firmware/` 当前没有实际服务代码文件。

### 1.2.1 服务实现状态表

| 模块 | 当前状态 | 说明 |
|------|------|------|
| `api-gateway` | 部分正式实现 | `health`、manifest、鉴权、限流、search 代理可用；`quote`、`checkout`、部分 wallet 仍有占位返回 |
| `catalog` | 正式服务代码存在 | 有 `cmd/main.go`，包含 search、rag-search、商品 CRUD 路由 |
| `ledger` | 正式服务代码存在 | 有 `balance`、`entries` 路由 |
| `mint` | 正式服务代码存在 | 有 deposit / redeem / reconcile 路由 |
| `chain-adapter` | 正式服务代码存在 | 有 reserve、tx、simulate-deposit；watcher 里部分仍是 skeleton / 开发向逻辑 |
| `audit` | 正式服务代码存在 | 管理端审计查询 / 写入接口存在 |
| `provisioning` | 正式服务代码存在 | 管理端开通 / 状态查询、CLI access 接口存在 |
| `quote` | 业务代码存在 | 目前未看到独立服务入口，更多是内部模块 |
| `checkout` | 业务代码存在 | 目前未看到独立服务入口，更多是内部模块 |
| `payment` | 仅目录 | 当前未见实际服务代码文件 |
| `firmware` | 仅目录 | 当前未见实际服务代码文件 |
| `test-mock-server.cjs` | 可运行 mock 实现 | 当前最完整的端到端演示流主要在这里 |

### 1.3 已确认存在的核心能力

- API Gateway：manifest、鉴权、限流、schema 校验、签名中间件已存在。
- Catalog：搜索、商品 handler、hybrid search / RAG / embedding 代码已存在。
- Quote / Checkout：报价、签名、幂等、状态机、outbox、隔离级别优化代码已存在。
- Ledger：双分录账本模型、仓储、服务、测试已存在。
- Mint / Redemption / Reconciliation：模型、handler、repository、service 已存在。
- Chain Adapter：watcher、Solana 适配、对账、deploy CLI 已存在。
- Audit / Provisioning：独立服务代码已存在。

### 1.4 前端与 Agent 侧现状

已确认存在：

- `firmware/components/` Web Components 源码与构建产物
- `agents/node-local-renderer/` 本地渲染器源码与构建产物
- `firmware/templates/animated-retail/` HTML/Vue 模板包
- `AGENT_EMBEDDING.md`
- `DESIGN.md`

这说明：

- “零固定前端 + 本地渲染”不是停留在概念层。
- Animated Retail 模板已经做过本地化沉淀，不只是 Stitch 导出的参考稿。

### 1.5 测试与辅助环境

已确认存在：

- `tests/contract/`
- `tests/security/`
- `tests/load/`
- `test-mock-server.cjs`
- `test-sandbox.cjs`
- `test-e2e-full.cjs`

可保守表述为：

- 项目已具备合约测试、安全测试、负载测试文件与 mock / sandbox 辅助环境。

### 1.6 AGP / Phase 6 的真实状态

已确认落地的部分：

- `schemas/*.json` 已出现 `AGP`
- `services/quote/internal/model/quote.go` 默认币种已是 `AGP`
- `services/mint/internal/model/mint.go` 已有 AgentPay 常量
- `onchain/agentpay-mint/` 已存在完整链上项目
- `test-mock-server.cjs` 已包含 AGP、escrow、dispute / DAO 相关逻辑
- `escrow-handlers.cjs` 已提供 escrow 路由处理
- 渲染器前端默认币种显示也已出现 `AGP`

暂时不能写成“完整完成”的部分：

- Solana Pay / Escrow / DAO 目前主要体现在 `test-mock-server.cjs`、`escrow-handlers.cjs` 与链上脚本层。
- 尚未看到与这些能力对应的完整 Go 正式服务实现。
- 因此更适合写成“原型验证中 / mock 已覆盖部分流程 / 待正式服务化”。

### 1.7 推荐对外口径

- Phase 1-5：核心协议、账本、结算、链适配、审计、服务开通与测试资产已基本成型。
- AGP 更名：已部分落地，并进入 schema、部分服务代码、mock 与链上项目。
- Phase 6：处于“原型验证与接口收敛”阶段，部分能力已通过 mock / 脚本验证，尚待收敛为正式服务。

### 1.8 本次功能校验结果

本次不是按 `git` 提交判断，而是按“代码存在 + 脚本可解析 + 实际请求可返回”来校验。

已完成的直接校验：

- `node --check test-mock-server.cjs` 通过
- `node --check escrow-handlers.cjs` 通过
- 临时启动 `test-mock-server.cjs` 后，以下请求已实测成功：
  - `GET /health`
  - `GET /.well-known/agent-rules.json`
  - `GET /api/v1/cli/search`
  - `POST /api/v1/cli/quote`
  - `POST /api/v1/cli/checkout/prepare`
  - `POST /api/v1/cli/checkout/commit`
  - `GET /api/v1/escrow/status`

本次实测返回结果要点：

- `/health` 返回 `status = ok`
- manifest 中已包含 `supported_assets` 和 `payment_rails`
- search 首个商品价格币种为 `AGP`
- quote / checkout prepare 返回币种均为 `AGP`
- checkout commit 返回 `status = committed`
- commit 响应中 `transaction_recorded = true`
- escrow 状态查询返回 `status = locked`

### 1.9 已发现的实现偏差

以下是文档和实际实现之间已经确认的偏差，后续文档应明确标注：

- `services/api-gateway/cmd/main.go` 中，`quote`、`checkout`、`wallet` 多数仍是占位 handler，主网关层不是完整实现。
- 真正可跑通的完整交易流，目前更多依赖 `test-mock-server.cjs`，不是 API Gateway 直连正式后端全链路。
- `services/chain-adapter/cmd/main.go` 默认端口冲突已修正为 `8084`。
- `services/provisioning/cmd/main.go` 默认端口冲突已修正为 `8085`。
- 当前终端环境里 `go` 命令不可用，因此这次**无法**用 `go test ./...` 做进一步运行验证；这不代表 Go 代码有问题，只代表本次环境缺少 Go 工具链。

---

## 2. 总体方向

当前项目围绕 **ANCF Zero-Frontend Commerce** 展开：平台不提供传统重型商城前端，而是通过 Agent 发现 manifest、获取商品 JSON、按固定固件/模板在本地临时渲染交互界面，再把用户意图交回后端完成 quote、checkout、支付、账本和服务开通。

核心边界已经明确：

- 本地 HTML / Vue / Web Components 只负责展示和意图采集。
- search 价格、库存、商品文案都不是可信交易来源。
- checkout 必须经过后端 quote、checkout_prepare、钱包签名、checkout_commit。
- 账本、铸币、支付、服务开通以后端状态机为准。

---

## 3. 文档进度

### 3.1 工程总方案

文件：`demo.md`

已完成：

- 中文审阅版 + English Agent 执行版。
- 后端服务拆分。
- Discovery Manifest。
- Search / Quote / Checkout API。
- vUSDC 影子账本方案。
- 可选链上 vUSDC 铸币方案。
- mint-service 状态机。
- 双分录账本设计。
- checkout 事务边界。
- Agent 约束。
- 安全措施。
- 测试清单。
- Alipay A2A 支付收款 Agent 对标方案。

Alipay A2A 已被定位为 ANCF 的 `payment rail`，而不是替代 ANCF：

- ANCF 负责 commerce protocol。
- Alipay A2A 负责 fiat payment provider / 支付 Skill / 用户授权 / 收款。

### 3.2 前端设计规范

文件：`DESIGN.md`

已包含：

- ANCF 前端架构。
- Web Components 固件设计。
- `<ancf-theme>` / `<ancf-search>` / `<ancf-quote>` / `<ancf-checkout>` 组件规范。
- Agent Bridge 协议。
- 页面交互流程。
- CSP 和组件安全约束。
- 文件输出约定。
- Mock API 端点。
- Gemini / AI Agent 实现检查清单。

---

## 4. 已有工程结构

当前仓库已有主要结构：

```text
schemas/
openapi/
firmware/
  components/
  templates/
agents/
  node-local-renderer/
services/
infra/
tests/
```

关键文件：

- `openapi/ancf.v1.yaml`
- `schemas/manifest.schema.json`
- `schemas/search-response.schema.json`
- `schemas/quote.schema.json`
- `schemas/checkout.schema.json`
- `schemas/mint.schema.json`
- `test-mock-server.cjs`
- `agents/node-local-renderer/src/agent.ts`
- `firmware/components/src/*.ts`

---

## 5. Stitch / 前端模板过程记录

这一节主要保留过程信息，方便回溯设计来源。

### 5.1 Stitch MCP 接通状态

已确认 Stitch MCP 可用：

- Stitch endpoint：`https://stitch.googleapis.com/mcp`
- MCP `initialize` 成功。
- `tools/list` 可用。
- 可用工具包括：
  - `create_project`
  - `get_project`
  - `list_projects`
  - `list_screens`
  - `get_screen`
  - `generate_screen_from_text`
  - `edit_screens`
  - `generate_variants`
  - `upload_design_md`
  - `create_design_system`
  - `create_design_system_from_design_md`
  - `update_design_system`
  - `list_design_systems`
  - `apply_design_system`

### 5.2 Stitch 项目

项目：

```text
Title: ANCF 商品搜索
ID: 9907418127151602795
```

已上传本地 `DESIGN.md` 到 Stitch，并创建设计系统：

```text
Design System Asset:
assets/4440324e02294a1b8e4be24f7549bf02
```

### 5.3 已生成的控制台页面

已生成并下载过：

- `ANCF Unified Rendering Console`
- `ANCF Quote & Checkout Console`
- `ANCF Merchant Product Upload Console`
- `ANCF Catalog Admin`
- `ANCF Agent Product Stream Console`
- `ANCF Checkout Confirmation`

本地目录：

```text
.stitch/designs/assets/
```

这些主要用于后台、统一渲染控制台、商户上传后台和结算确认设计参考。

### 5.4 Stitch Animated Retail 素材下载

按用户指定，从 Stitch 项目中获取了 4 个 screen 的图片和 HTML：

```text
Project: ANCF 商品搜索
ID: 9907418127151602795
```

Screens：

```text
1. ANCF Catalog (Animated Retail)
   ID: 4223438297438515640

2. ANCF Catalog (Animated Retail)
   ID: 9f4d1029c9be478298bcc35ca175ecd1

3. ANCF Product Detail (Animated)
   ID: 6837659765762598684

4. ANCF Product Detail (Animated)
   ID: 2302bb7157b84820ac9e22abb58d1311
```

下载目录：

```text
.stitch/designs/stitch-animated-retail/
```

已下载文件：

```text
ancf-catalog-animated-retail-42234382.html
ancf-catalog-animated-retail-42234382.png
ancf-catalog-animated-retail-42234382.screen.json

ancf-catalog-animated-retail-9f4d1029.html
ancf-catalog-animated-retail-9f4d1029.png
ancf-catalog-animated-retail-9f4d1029.screen.json

ancf-product-detail-animated-68376597.html
ancf-product-detail-animated-68376597.png
ancf-product-detail-animated-68376597.screen.json

ancf-product-detail-animated-2302bb71.html
ancf-product-detail-animated-2302bb71.png
ancf-product-detail-animated-2302bb71.screen.json

stitch-screens.manifest.json
```

说明：

- 原始 Stitch HTML 使用了 Tailwind browser runtime、Google Fonts、Material Symbols 等外部依赖。
- 这些原始文件保留为视觉参考，不建议直接作为 Agent 一次性渲染模板。

### 5.5 已转换的固定模板素材包

新增目录：

```text
firmware/templates/animated-retail/
```

目标：

- 把 Stitch 的 Animated Retail 页面转成 AI / Agent 可快速使用的固定模板。
- 用于一次性本地 HTML 交互页面。
- 同时提供 Vue SFC 版本，便于后续集成到 Vue 应用或生成式前端壳。
- 去掉外部 CDN 运行依赖，降低 token 和渲染负担。

#### Vue 组件

```text
firmware/templates/animated-retail/vue/AncfAnimatedCatalog.vue
firmware/templates/animated-retail/vue/AncfAnimatedProductDetail.vue
```

组件设计：

- `AncfAnimatedCatalog.vue`
  - 商品列表 / 搜索 / 筛选
  - 支持 `products` props
  - 支持 `select` / `quote` / `back` 事件
  - 商品字段兼容 ANCF search response

- `AncfAnimatedProductDetail.vue`
  - 商品详情 / 图片 / gallery / specs / price / stock
  - 支持 `product` props
  - 支持 `select` / `quote` / `back` 事件
  - 明确展示 backend quote required

#### 一次性 HTML 模板

```text
firmware/templates/animated-retail/html/ancf-animated-catalog.template.html
firmware/templates/animated-retail/html/ancf-animated-product-detail.template.html
```

特点：

- 无 Tailwind CDN
- 无 Google Fonts
- 无 Material Symbols
- 无外部 JS runtime
- Agent 只需要替换：

```html
<script id="ancf-payload" type="application/json">
  ...
</script>
```

即可本地渲染。

#### 模板 manifest

```text
firmware/templates/animated-retail/template-manifest.json
```

包含：

- template pack 信息
- Stitch source screen 引用
- `animated_catalog` 模板定义
- `animated_product_detail` 模板定义
- payload schema
- 事件列表
- Agent 安全约束

#### Agent 嵌套规范

```text
firmware/templates/animated-retail/AGENT_EMBEDDING.md
```

已定义：

- 何时使用 Catalog 模板
- 何时使用 Product Detail 模板
- Agent 如何注入 JSON payload
- 模板会触发哪些事件
- Agent 如何把模板事件映射成 ANCF bridge command
- 安全规则
- 为什么不直接使用 Stitch 原始 HTML

#### README

```text
firmware/templates/animated-retail/README.md
```

用于快速说明模板包用途和文件结构。

### 5.6 模板事件规范

一次性 HTML 模板会触发：

```text
ANCF_TEMPLATE_SELECT
ANCF_TEMPLATE_QUOTE
ANCF_TEMPLATE_BACK
```

事件语义：

- `ANCF_TEMPLATE_SELECT`
  - 用户选中商品
  - 可进入详情页或更新右侧 inspector

- `ANCF_TEMPLATE_QUOTE`
  - 用户请求报价
  - Agent 必须调用 `POST /api/v1/cli/quote`
  - 不能直接下单

- `ANCF_TEMPLATE_BACK`
  - 本地页面导航
  - 不触发后端交易

如果存在 `window.AgentBridge`，模板也会发送：

```json
{
  "command": "ANCF_TEMPLATE_QUOTE",
  "payload": {}
}
```

注意：模板不会直接调用后端 API，也不会执行 checkout。

### 5.7 Agent 嵌套执行流程

推荐流程：

```text
1. Agent 获取并校验 /.well-known/agent-rules.json
2. Agent 校验 manifest 签名、过期时间、firmware SRI
3. Agent 调用 ancf:search 或 GET /api/v1/cli/search
4. Agent 选择 animated_catalog 模板
5. Agent 把 search response 转成 template payload
6. Agent 注入 script#ancf-payload
7. Agent 从 127.0.0.1 临时服务渲染 HTML
8. 用户点击 Quote
9. HTML 发出 ANCF_TEMPLATE_QUOTE
10. Agent 调用 POST /api/v1/cli/quote
11. 后续进入 quote / checkout 固件流程
```

### 5.8 已完成校验

已检查：

- `template-manifest.json` 可正常 JSON parse
- 两个 HTML 模板没有 `cdn`、`googleapis`、`tailwind`、`Material Symbols`、`script src`、`link href` 外部依赖
- Vue 组件包含明确 props 和 emits
- 原始 Stitch HTML/PNG 已下载到本地

校验命令结果：

```text
manifest ok
```

### 5.9 前端部分建议下一步

优先级建议：

1. 把 `animated-retail` 模板接入 `agents/node-local-renderer`，增加路由：

```text
/templates/animated-retail/catalog
/templates/animated-retail/detail/:sku_id
```

2. 增加 Agent payload 生成器：

```text
search response -> animated_catalog payload
selected item -> animated_product_detail payload
```

3. 给 `test-mock-server.cjs` 扩展更多 SKU，至少 12 个商品，用于验证列表浏览性能。

4. 给模板增加 Playwright 截图检查：

```text
desktop 1440x960
mobile 390x844
```

5. 后续再将 Vue SFC 接入真正的 Vue 示例工程或生成式前端构建流程。

---

## 6. 详细阶段记录

这一节保留原始阶段信息，但阅读时请以前面的“已交叉验证结论”为准。

### Phase 1: 协议和本地 demo ✅ 已完成

#### SUB-001 项目脚手架与基础设施

- Go module (`github.com/ancf-commerce/ancf`, Go 1.22)
- Docker Compose (PostgreSQL 16 + Redis 7 + api-gateway)
- 多阶段 Dockerfile
- Makefile
- `.env.example`

#### SUB-002 JSON Schemas 与 OpenAPI 规范

- `schemas/manifest.schema.json`
- `schemas/search-response.schema.json`
- `schemas/quote.schema.json`
- `schemas/checkout.schema.json`
- `schemas/mint.schema.json`
- `openapi/ancf.v1.yaml`

#### SUB-003 数据库 Schema 与迁移

- `001_init.sql`
- `001_init_rollback.sql`
- `002_seed.sql`
- SKU / Quote / Ledger 模型
- PostgreSQL full-text search / GIN / tsvector

#### SUB-004 API Gateway 中间件服务

- API Key 鉴权
- Token bucket 限流
- JSON Schema 请求体验证
- 请求日志中间件
- CORS + CSP
- HTTP 签名校验
- `GET /.well-known/agent-rules.json`
- `GET /health`

#### SUB-005 Catalog Service 与 Search API

- PostgreSQL full-text search
- Repository → Service → Handler
- `GET /api/v1/cli/search?q=H100&limit=5`
- 参数校验

#### SUB-006 Quote Service 与 Checkout API

- `POST /api/v1/cli/quote`
- `POST /api/v1/cli/checkout/prepare`
- `POST /api/v1/cli/checkout/commit`
- 报价原子消费
- 幂等键 body-hash 冲突检测

#### SUB-007 Web Components 固件与 Node Agent 渲染器

- `<ancf-theme>`
- `<ancf-search>`
- `<ancf-quote>`
- `<ancf-checkout>`
- `agent-bridge`
- Node Agent 本地渲染器

### Phase 2: 可信结算 ✅ 已完成

#### SUB-008 Ledger Service 双分录账本

- `services/ledger/internal/repository/ledger_repository.go`
- `services/ledger/internal/service/ledger_service.go`
- `services/ledger/internal/handler/balance_handler.go`
- `services/ledger/cmd/main.go`

#### SUB-009 Checkout 硬化与状态机

- EdDSA 签名验证
- 8 状态严格流转
- 三路幂等解析
- CommitCheckout 事务重写

#### SUB-010 库存并发锁 + Outbox

- `services/checkout/internal/repository/outbox_repository.go`
- `services/checkout/internal/service/outbox_processor.go`
- `services/catalog/internal/repository/sku_repository.go`

#### SUB-011 集成测试

- 签名测试
- 状态机测试
- 幂等测试
- 账本测试
- 合约测试骨架

### Phase 3: 充值与赎回 ✅ 已完成

#### SUB-012 Mint Service

- 9 状态铸币状态机
- Deposit intent / confirm / status / reserve info

#### SUB-013 Redemption Service

- 8 状态赎回状态机
- payout / release / balance lock

#### SUB-014 Chain Adapter

- Solana deposit watcher
- DepositEventHandler 解耦
- 32 confirmations

#### SUB-015 储备对账 + Mock API

- ReconciliationService
- mock reserve
- mock endpoints 扩展

### Phase 4: Audit / Provisioning / Solana / 测试 ✅ 已完成

#### SUB-016 Audit Service

- INSERT-only audit log
- actor / resource / action 常量

#### SUB-017 Provisioning Service

- Outbox 驱动开通
- settle / refund

#### SUB-018 Solana Chain Integration

- `token2022.go`
- `multisig.go`
- `deposit_watcher.go`
- `reconciliation.go`
- `cmd/deploy/main.go`

#### SUB-019 安全测试 + 负载测试

- `tests/security/`
- `tests/load/`
- `tests/contract/mint_redemption_test.go`

### Phase 5: 链上完善 + 并发优化 ✅

#### SUB-020 跨系统 Outbox 最终一致性

- `services/chain-adapter/internal/service/deposit_processor.go`
- `processEvent` 事务内写入 chain_txs + outbox
- deposit_tx_id 去重

#### SUB-021 并发优化

- `SERIALIZABLE` → `READ COMMITTED`
- `FOR UPDATE SKIP LOCKED`
- CASE-WHEN 批量扣减
- OutboxProcessorV2
- `003_performance.sql`

#### SUB-022 Rust 铸币项目完善

- `onchain/vusdc-mint/`
- `--cluster devnet/testnet/mainnet`
- compute budget / priority fee / retry
- deploy / multisig / verify scripts

### 当前测试环境状态

#### Devnet 铸币测试（待执行）

| 项目 | 状态 |
|------|------|
| Rust 工具链 | ✅ |
| Solana devnet RPC 连接 | ✅ |
| JS / Rust 铸币脚本 | ✅ |
| payer keypair 在本地 | ✅ |
| 目标钱包 devnet SOL | ✅ |
| payer 余额 | ❌ |
| 阻塞原因 | devnet 水龙头限流 |

### Phase 6: Agent 统一认证 + AgentPay + 支付集成 🚧

这一阶段请以“部分原型已出现、正式服务未完成”来理解。

#### 当前已写入文档/原型的方向

1. Agent 统一认证
2. AgentPay (AGP) 更名
3. Solana Pay 集成
4. 托管钱包 Escrow
5. 纠纷 DAO

#### 文档中规划过的端点

| 方法 | 路径 | 模块 |
|------|------|------|
| POST | `/api/v1/agents/register` | Agent |
| POST | `/api/v1/agents/token` | Agent |
| GET | `/api/v1/agents/{agent_id}` | Agent |
| PUT | `/api/v1/agents/{agent_id}/wallet` | Agent |
| POST | `/api/v1/agents/{agent_id}/sku` | Agent |
| POST | `/api/v1/cli/payments/solana-pay` | Solana Pay |
| GET | `/api/v1/cli/payments/solana-pay/{id}` | Solana Pay |
| POST | `/api/v1/cli/payments/solana-pay/webhook` | Solana Pay |
| POST | `/api/v1/escrow/lock` | Escrow |
| POST | `/api/v1/escrow/confirm-delivery` | Escrow |
| POST | `/api/v1/escrow/confirm-receipt` | Escrow |
| GET | `/api/v1/escrow/{escrow_id}` | Escrow |
| POST | `/api/v1/escrow/timeout-release` | Escrow |
| POST | `/api/v1/dao/disputes` | DAO |
| POST | `/api/v1/dao/vote` | DAO |
| GET | `/api/v1/dao/disputes/{id}` | DAO |
| GET | `/api/v1/dao/members` | DAO |
| POST | `/api/v1/dao/apply` | DAO |

#### AGP 更名记录

| 变更项 | 旧值 | 新值 |
|--------|------|------|
| 内部记账代币符号 | vUSDC | AGP |
| Schema 引用 | `"currency": "vUSDC"` | `"currency": "AGP"` |
| API 响应 | `"currency": "vUSDC"` | `"currency": "AGP"` |
| 前端显示 | vUSDC | AGP |

#### 支付 Rail 清单

| Rail | 币种 | 网络 | 类型 | 状态 |
|------|------|------|------|------|
| `agp_ledger` | AGP | internal | 影子账本 | 部分已落地 |
| `alipay_a2a` | CNY | Alipay | 法币支付 | 已规划 |
| `solana_pay` | USDC/USDT/AGP | Solana | 链上支付 | 原型/规划 |
| `ethereum` | USDC/USDT/AGP | Ethereum | 链上支付 | 后续 |

---

## 7. 后续维护规则

为了避免这份文档以后再次“越写越像宣传稿”，建议后续更新按下面规则：

1. 先更新“已交叉验证结论”，再更新后面的详细记录。
2. 凡是只存在于 `test-mock-server.cjs`、脚本、临时工具里的能力，统一标成“mock / 原型”。
3. 凡是没有独立服务入口的后端模块，不写成“可独立部署服务”。
4. 凡是规划中的端点，统一加”规划 / 原型”状态，不与正式实现混写。

---

## Phase 7: 主干拧紧 ✅ (task-2026-008, 2026-06-12)

### SUB-032 ✅ API Gateway 占位替换
- 创建 `services/api-gateway/internal/handler/proxy.go` — ReverseProxy + ProxyWithFallback (272行)
- gateway cmd/main.go: 5个占位handler → ReverseProxy 代理到 mock server
- 新增 5 个 wallet 路由 (balance/deposit-confirm/mint-status/redeem-status/reserve-info)
- 架构: Client → Gateway(:8080) → Mock(:9080)，Gateway 负责鉴权/限流/校验

### SUB-033 ✅ Quote + Checkout 独立服务 + 端口清理
- `services/quote/cmd/main.go` — quote 服务 (端口 8081)
- `services/checkout/cmd/main.go` — checkout 服务 (端口 8082)
- `services/PORTS.md` — 最终端口表 (无冲突)

| 服务 | 端口 |
|------|------|
| api-gateway | 8080 |
| quote | 8081 |
| checkout | 8082 |
| catalog | 8083 |
| chain-adapter | 8084 |
| provisioning | 8085 |
| ledger | 8086 |
| mint | 8087 |
| audit | 8089 |

### SUB-034 ✅ TRC20 + AGP 扫尾 + 清理
- PAYMENT_RAILS 新增 `USDT-trc20` (TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t)
- 5 个文件 AGP 更名扫尾 (AGENT_EMBEDDING, templates, Cargo.toml, sandbox)
- 创建 `.gitignore` (排除 node_modules/dist/key/日志/构建产物)
- 创建 `SERVICE_STATUS.md` — 可验证状态表 (正式/mock/骨架/规划)

### SUB-035 ✅ Chain Adapter 加固
- `watcher_robust.go` — cursor 持久化、重启恢复、WebSocket 重连、批量处理、健康检查
- `confirmations.go` — 确认管理、重试3次、60s超时、批量确认

### 修复的端口冲突
- mock server 端口: 硬编码8080 → `process.env.PORT || ‘8080’`
- Gateway 代理到 mock 默认用 9080 (避免冲突)

---

## Phase 8: 项目总结 + 全量中文注释 ✅ (2026-06-14)

### 完成进度核验（按实际代码扫描，非文档口径）
- 后端 Go 代码：11 个服务目录，其中 **9 个有实际代码 + 独立 `cmd/main.go`**（api-gateway/catalog/quote/checkout/ledger/mint/chain-adapter/provisioning/audit），共 89 个 `.go` 文件。
- `payment/`、`firmware/` 仅空目录，无 `.go` 文件 → 实际状态为“规划”，与 `SERVICE_STATUS.md` 标注的 Active 不符（已在总结中标注偏差）。
- 端口以各服务 `cmd/main.go` 默认值为准，与 `services/PORTS.md` 一致；`SERVICE_STATUS.md` 端口列已过时。
- 前端固件（Web Components / Vue / animated-retail 模板）、Node 本地渲染器、onchain Rust 项目（vusdc-mint / agentpay-mint）均存在实际源码。

### SUB-036 ✅ 项目总结文件
- 新增 `项目总结.md`：完成进度评估表、服务实现状态（代码核验口径）、**完整代码结构树**（逐文件职责标注）、分层架构约定、关键设计决策、安全措施、数据库、运行方式、已知偏差。

### SUB-037 ✅ 全量中文注释（ADD-ONLY）
- 对 9 个服务的 89 个 `.go` 文件补充简体中文注释，原则：只增不改，不翻译/不改写已有英文 GoDoc，不改动任何代码。
- 重点补齐：**包级文档注释**（此前 45/47 包缺失，仅 chain-adapter/solana 有）、未注释的导出符号、零注释的测试文件（`deposit_processor_test.go`、`internal_auth_test.go`）。
- 由 7 个并行子代理按服务分工完成（api-gateway / catalog / ledger+quote / mint / checkout / audit+provisioning / chain-adapter）。

### 顺带修复
- `services/mint/internal/service/redemption_service.go` 文件头部的 UTF-8 BOM（`EF BB BF`）已移除，避免 `package` 前的隐藏字符影响工具链与 diff。

### 复核建议
- 本环境无 `go`/`gofmt` 工具链；注释为纯新增不影响编译。建议在具备 Go 1.22 的环境执行 `gofmt -l ./services` 与 `go build ./...` 做一次复核。


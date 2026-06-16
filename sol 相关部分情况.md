# SOL 相关部分情况

> 审查范围：代币铸造（mint）、代币发行/充值入账、双分录记账（ledger）、多签投票治理（multisig）相关代码
> 审查依据：`/opt/ancf/非必要文件/.agents/skills/solana-dev`（Solana 安全审查清单 + Token-2022 审查清单）
> 审查日期：2026-06-14
> 审查对象（commit）：`7ff152c`（main 分支工作区）

---

## 0. 一句话结论

当前 SOL 相关代码处于 **"影子账本（shadow-ledger）+ Phase 4 链上骨架"** 的半成品状态：链上铸造/销毁（`MintTo`/`Burn`/`DeployVUSDC`）全部是返回错误的占位实现，真正影响资金安全的是**链下充值检测 → 记账入账**这条链路。审查发现 **1 个严重（Critical）、3 个高危（High）、4 个中危（Medium）** 问题，其中多处具备典型的 "AI 编程代码偏差" 特征：**定义了安全常量却从不使用、注释承诺的校验在代码里缺失、编码格式前后不一致导致整段安全逻辑静默失效**。

---

## 1. 问题总表

| 编号 | 严重度 | 模块 | 问题 | AI 偏差特征 |
|------|--------|------|------|-------------|
| S-01 | 🔴 Critical | 充值检测 | `decodeSPLTransfer` 完全不校验 token mint 地址，任何 SPL 代币转入储备地址都会被当作 USDC 入账并铸出等额 vUSDC | 定义了 `USDCMainnetMint` 常量却从不使用 |
| H-01 | 🟠 High | 多签投票 | `ApproveProposal` 把 hex 公钥当 ed25519 公钥用 + 签名消息与提案绑定不全，签名校验形同虚设 | 编码格式（base58/hex）前后不一致 |
| H-02 | 🟠 High | 多签投票 | 提案 nonce 仅内存自增、`MintAddress` 恒为空串，重启后 proposalID 可碰撞、跨提案签名可重放 | 注释承诺"绑定 amount/dest/nonce"但实现遗漏字段 |
| H-03 | 🟠 High | 记账 | `ValidateBalance` 借贷恒等永远返回 true，双分录核心不变式校验失效 | 复制了求和模板但两边加了同一个变量 |
| M-01 | 🟡 Medium | 充值检测 | 确认数 `currentSlot - BlockNumber` 无符号下溢，RPC 抖动时可绕过 minConfirmations | 缺少边界检查 |
| M-02 | 🟡 Medium | 充值检测 | 储备余额按 watcher 解析的明文金额累加，未做 Token-2022 转账费 delta 核算 | 套用经典 SPL 1:1 假设 |
| M-03 | 🟡 Medium | 多签执行 | `ExecuteProposal` 先标记 executed 再发链上交易，回滚依赖内存且无幂等键 | 乐观状态机缺持久化原子性 |
| M-04 | 🟡 Medium | 铸造授权 | mint 入账完全不要求多签批准，multisig 与 mint 链路彻底脱节 | 两套机制各写各的，从未对接 |

---

## 2. 🔴 S-01（Critical）：充值检测不校验 token mint —— 任意代币可铸出 vUSDC

### 位置
[deposit_watcher.go:351-396](services/chain-adapter/internal/solana/deposit_watcher.go#L351-L396)，`decodeSPLTransfer`

### 问题
充值检测的逻辑是：遍历交易的 `postTokenBalances`，找到 owner 等于储备地址、且 post 余额 > pre 余额的条目，把差额当作充值金额：

```go
for _, postBal := range tx.Meta.PostTokenBalances {
    owner := postBal.Owner
    if owner != reserveAddr {
        continue
    }
    // ... 找到对应 preBal，算出 amount = postAmount - preAmount
    return &model.DepositEvent{
        AmountMinor: int64(amount),
        AssetSymbol: assetSymbol, // ← 直接用轮询时传入的符号，不是从链上 mint 推断
        ...
    }
}
```

整段逻辑里 **`postBal.Mint` 只被用来匹配 pre/post 配对**（[:365](services/chain-adapter/internal/solana/deposit_watcher.go#L365)、[:392](services/chain-adapter/internal/solana/deposit_watcher.go#L392)），**从未与真实的 USDC mint 地址比对**。代码顶部明明定义了：

```go
USDCDevnetMint  = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
USDCMainnetMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
```

这两个常量在整个 chain-adapter 里 **零引用**（已 grep 确认）。

### 攻击路径
1. 攻击者创建一个自己的 SPL / Token-2022 mint（成本几乎为零，余额随意铸）。
2. 向平台储备地址转入 1,000,000 单位的"山寨"代币。
3. watcher 看到储备地址 token 余额增加 → 生成 `DepositEvent{AmountMinor: 1000000, AssetSymbol: "vUSDC"}`。
4. `processEvent` 把它写进 `chain_txs.raw_json` 并发 `deposit_detected` 事件，同时 `IncrementReserveConfirmedWithTx` 把储备"确认余额"加了 100 万。
5. mint 服务 `ConfirmDeposit` 给攻击者钱包贷记 100 万 vUSDC。

**结果：用零成本的假币铸出等额真实 vUSDC，直接击穿储备金锚定。** 这是整个稳定币系统的命根子。

### 为什么 mint 服务的"proof 校验"挡不住
`mint_service.go` 的 `ConfirmDeposit` 看似严谨——它调 `GetFinalizedDepositProofForUpdate` 把金额、地址、网络逐项比对（[mint_service.go:206-230](services/mint/internal/service/mint_service.go#L206-L230)）。但这份 proof 来自 [mint_repository.go:361-387](services/mint/internal/repository/mint_repository.go#L361-L387)，读的是 `chain_txs.raw_json`，**而该 JSON 正是 watcher 在第 4 步写进去的同一个 `DepositEvent`**。等于"拿嫌疑人自己的口供给自己作证"。

更关键的是，`model.ChainDepositProof` 结构体（[mint.go:129](services/mint/internal/model/mint.go#L129)）**根本没有 `mint` 字段**：

```go
type ChainDepositProof struct {
    Network, TxHash, FromAddress, ToAddress string
    AmountMinor int64
    AssetSymbol string  // ← 只有符号字符串，没有链上 mint 地址
    ...
}
```

所以从 watcher 到 ledger 的整条链路上，**没有任何一处持有或校验过真实的 mint pubkey**。这完全命中 skill `security.md` 的 Token-2022 审查项 #6（Transfer Hook）与客户端清单 "Validate token mint ↔ token account relationships"——只是这里连基础 SPL 的 mint 校验都没有。

### 修复
在 `decodeSPLTransfer` 里，对每个候选 `postBal` 增加 mint 白名单校验，并用配置注入而非硬编码：

```go
expectedMint := w.assetMints[assetSymbol] // 启动时从配置加载，区分 devnet/mainnet
if postBal.Mint != expectedMint {
    continue // 非目标 mint，忽略
}
```

同时把 `mint` 字段加入 `DepositEvent` / `ChainDepositProof`，让 mint 服务能独立复核（即便仍读 raw_json，至少应在 watcher 入库前就拒绝非法 mint）。

---

## 3. 🟠 H-01（High）：多签投票签名校验形同虚设 —— hex/base58 编码错位

### 位置
[multisig.go:322-359](services/chain-adapter/internal/solana/multisig.go#L322-L359)，`ApproveProposal`

### 问题
这是投票机制的核心。`ApproveProposal` 在 F-004-04 修复里"加了"ed25519 签名校验，但实现有两处致命错位：

**(1) 公钥编码不一致。** 校验时这样取公钥：

```go
approverPubKey, err := hex.DecodeString(approver.PublicKey)
...
if !ed25519.Verify(approverPubKey, sigMsgHash[:], sigBytes) { ... }
```

它假设 `approver.PublicKey` 是 **hex** 编码。但 Solana 公钥的通用编码是 **base58**，而且 `MultisigConfig.Signers` 注释明确写的是 `// 3 public key addresses (base58)`（[multisig.go:31](services/chain-adapter/internal/solana/multisig.go#L31)）。`isSigner` 比对用的也是原始字符串。

- 若签名者地址按设计存成 base58，`hex.DecodeString` 要么直接报错（含 base58 特有字符如 `0`/`O`/`l`），要么解析出长度不对的字节串，`ed25519.Verify` 永远返回 false → **所有合法审批都被拒**（可用性崩溃）。
- 唯一能"通过"的路径是上游恰好喂了 64 hex 字符的公钥（如 deploy CLI 的 `loadKeypair` 用 `fmt.Sprintf("%x", publicKey)` 产出 hex）——**系统内部公钥编码本身就自相矛盾**（deploy 产 hex，multisig 注释要 base58）。

**(2) 校验对象是哈希而非原文。** 它对 `sigMsgHash[:]`（SHA-256 摘要）做 `ed25519.Verify`。标准 ed25519 是对**原始消息**签名（内部自带哈希），这里却让签名者对"摘要"再签一次。除非签名方也按同样的非标准方式签，否则永远验不过；若双方都这么做，则等于自定义了一套绕过标准库语义的方案。

### AI 偏差特征
典型的"安全修复看起来很到位、实则编码假设与系统其它部分对不上"。注释、字段说明、deploy 工具、校验函数四处对公钥编码的假设各不相同（base58 vs hex），是多段 AI 生成代码拼接时未对齐契约的标志。

### 修复
- 统一公钥编码：Solana 场景应全程用 base58（引入 `base58.Decode`），`Signer.PublicKey`、`MultisigConfig.Signers`、deploy CLI 输出全部对齐。
- 用 `ed25519.Verify(pub, message, sig)` 校验**原始 canonical message 字节**，不要自己先 SHA-256。
- 加单元测试：真实 keypair 签名 → 通过；改一字节 → 失败；换编码 → 明确报错。

---

## 4. 🟠 H-02（High）：提案 ID 可碰撞、跨提案签名可重放

### 位置
[multisig.go:166-180](services/chain-adapter/internal/solana/multisig.go#L166-L180)（nonce/ID 派生）、[multisig.go:264-306](services/chain-adapter/internal/solana/multisig.go#L264-L306)（ProposeMint）

### 问题
**(1) nonce 仅内存自增，重启回绕。** `nextNonce` 是 `m.nonce++`，启动时从 DB 取 `MAX(nonce)` 恢复（[multisig.go:766](services/chain-adapter/internal/solana/multisig.go#L766)）。但 `loadProposalsFromDB` 失败时只 `Warn` 不中止（[multisig.go:151](services/chain-adapter/internal/solana/multisig.go#L151)），nonce 归零，新提案 ID 与历史碰撞。`saveProposalToDB` 用 `ON CONFLICT (id) DO UPDATE`（[multisig.go:683](services/chain-adapter/internal/solana/multisig.go#L683)），碰撞会**静默覆盖**已有提案的 approvals/status。

**(2) MintAddress 恒为空，签名消息维度缺失。** `ProposeMint` 构造提案时 **从不设置 `MintAddress`**（结构体有该字段，ProposeMint 没赋值，[multisig.go:278-288](services/chain-adapter/internal/solana/multisig.go#L278-L288)），执行 `MintTo` 时传的是空 mint（[multisig.go:467](services/chain-adapter/internal/solana/multisig.go#L467)）。

更要命的是审批签名消息：

```go
sigMsg := fmt.Sprintf("%s|%s|%d|%d", proposalID, proposal.Action, proposal.Amount, proposal.Nonce)
```

它绑定了 proposalID/action/amount/nonce，但 **没绑定 destAddress 和 mintAddress**——"钱打给谁"这个关键字段不在签名保护范围内。配合 (1) 的 ID 覆盖，存在构造同 ID、不同 dest 的提案、复用旧签名的空间。

### 修复
- nonce 改为 DB 序列或带 `proposer` 维度的持久计数；`loadProposalsFromDB` 失败应 fail-fast。
- 签名消息纳入全部资金语义字段：`proposalID|action|amount|destAddress|mintAddress|nonce`，与 `deriveProposalID` 字段集一致。
- `ProposeMint` 必须显式设置 `MintAddress`（从配置注入 vUSDC mint），否则拒绝建提案。

---

## 5. 🟠 H-03（High）：双分录借贷恒等校验永远为真

### 位置
[ledger.go:106-113](services/ledger/internal/model/ledger.go#L106-L113)，`ValidateBalance`

### 问题
双分录记账最根本的不变式是"借方合计 = 贷方合计"。这个函数本应校验它：

```go
func ValidateBalance(entries []LedgerEntry) bool {
    var totalDebit, totalCredit int64
    for _, e := range entries {
        totalDebit += e.AmountMinor   // ← 两边加的都是 e.AmountMinor
        totalCredit += e.AmountMinor  // ← 同一个值
    }
    return totalDebit == totalCredit  // ← 恒为 true
}
```

`LedgerEntry` 的模型是"一行同时记 `DebitAccount` 和 `CreditAccount`、共用一个 `AmountMinor`"（行内借贷自平），所以这个按"行求和"的校验**对任何输入都返回 true**——包括金额为负、账户类型非法、借贷账户写反的分录。它没有起到任何校验作用。

### 影响与定性
- 当前 `ValidateBalance` 只在 `ledger_service_test.go` 里被调用（[:45](services/ledger/internal/service/ledger_service_test.go#L45)、[:173](services/ledger/internal/service/ledger_service_test.go#L173)），生产 `PostTransaction` 路径并不依赖它。所以**暂未直接造成资金错账**，定为 High 而非 Critical。
- 但它制造了**虚假的安全感**：测试用它断言"分录平衡"，绿灯通过其实什么都没验证。一旦后续有人在生产路径加上 `if !ValidateBalance(...)` 作为入账闸门，会以为有保护实则敞开。

### AI 偏差特征
非常典型——AI 套用了"两个累加器分别求和再比较"的模板，但在这个"行内自平"的数据模型下，应该按 `DebitAccount`/`CreditAccount` 分组累加（或校验跨行的科目轧差），结果两个累加器加了同一个字段。模板对、语义错。

### 修复
按账户维度聚合再校验，例如：

```go
func ValidateBalance(entries []LedgerEntry) bool {
    bal := map[string]int64{}
    for _, e := range entries {
        if e.AmountMinor <= 0 { return false } // 金额必须为正
        bal[e.DebitAccount] -= e.AmountMinor
        bal[e.CreditAccount] += e.AmountMinor
    }
    var sum int64
    for _, v := range bal { sum += v }
    return sum == 0 // 全局借贷轧差为零
}
```

并在生产 `PostTransaction` 入口实际调用它。

---

## 6. 🟡 M-01（Medium）：确认数无符号下溢，可绕过最小确认数

### 位置
[deposit_watcher.go:278-283](services/chain-adapter/internal/solana/deposit_watcher.go#L278-L283)

### 问题
```go
currentSlot, _ := rpcClient.GetSlot(ctx, w.commitment)
if currentSlot > 0 && (currentSlot - uint64(event.BlockNumber)) < w.minConfirmations {
    // 确认数不足，跳过
}
```

`currentSlot` 和 `event.BlockNumber` 都是无符号数相减。`GetSlot` 用的是 `confirmed` commitment，而 `event.BlockNumber` 来自 `getTransaction`，两者来自不同 RPC 调用、可能命中不同节点。若 `currentSlot < BlockNumber`（RPC 节点轻微回退/抖动，完全可能），`currentSlot - BlockNumber` 会**下溢成一个接近 `2^64` 的巨大数**，远大于 `minConfirmations(32)`，于是**确认数检查被绕过**，未充分确认的交易被当作已 finalize 处理。`GetSlot` 的 error 还被 `_` 忽略，失败时 `currentSlot=0`，靠 `currentSlot > 0` 兜底，但抖动场景兜不住。

注意 `processEvent` 随后无条件把状态写成 `TxStatusFinalized`、确认数写成 `minConfirmations`（[deposit_watcher.go:469-470](services/chain-adapter/internal/solana/deposit_watcher.go#L469-L470)），等于"自封 finalized"，下游 mint 服务的 `confirmations < 32` 校验也就拿到了这个伪造值。

### 修复
```go
if currentSlot < uint64(event.BlockNumber) {
    continue // 槽位异常，等下一轮
}
if currentSlot - uint64(event.BlockNumber) < w.minConfirmations {
    continue
}
```
并处理 `GetSlot` 的 error，确认数应取实测值而非直接写 `minConfirmations`。

---

## 7. 🟡 M-02（Medium）：储备入账未做 Token-2022 转账费 delta 核算

### 位置
[deposit_watcher.go:490](services/chain-adapter/internal/solana/deposit_watcher.go#L490) `IncrementReserveConfirmedWithTx` + [decodeSPLTransfer](services/chain-adapter/internal/solana/deposit_watcher.go#L382)

### 问题
充值金额取 `postAmount - preAmount` 作为储备增量并直接累加到"确认储备余额"。这对经典 SPL Token 没问题，但平台自己的 vUSDC 是 **Token-2022**，而 USDC 充值若涉及带 `TransferFee` 扩展的 mint，到账金额会被在途扣费。skill `security.md` 审查项 #10 明确警告：

> Transfer fee taken in-flight... 你以为收到 100，实际到账 80，协议却记 100。

当前实现：
- 储备增量虽然取的是余额 delta（这一侧相对安全，因为是实测增量）；
- 但**没有任何地方校验"声明充值的 mint 是否带 TransferFee/TransferHook 扩展"**，也没有把扩展信息纳入对账。结合 S-01（mint 完全不校验），攻击者可用带 `permanent delegate` 扩展的假 mint 充值，事后用 delegate 把储备地址里的代币直接划走——skill 审查项 #12 描述的正是此场景。

### 修复
- 落实 S-01 的 mint 白名单后，对白名单 mint 在接入时一次性核验其 Token-2022 扩展集合（拒绝 `PermanentDelegate`、按 `TransferFee` 做 delta 核算）。
- 对账侧（`reconciliation.go`）增加"链上实际余额 vs 账面储备"的独立核对，而不仅是内部 liability 自洽。

---

## 8. 🟡 M-03（Medium）：多签执行先标记后上链，回滚仅靠内存

### 位置
[multisig.go:418-519](services/chain-adapter/internal/solana/multisig.go#L418-L519)，`ExecuteProposal`

### 问题
执行流程是："先在内存把 `Status` 置 `executed` → 释放锁 → 调 `MintTo` 上链 → 失败再把状态改回 `approved`"（[multisig.go:437-493](services/chain-adapter/internal/solana/multisig.go#L437-L493)）。问题：

1. 把状态改成 executed 后**先释放了锁**才上链，"标记 executed"这步**没有立即落库**（`saveProposalToDB` 在成功后才调，[multisig.go:509](services/chain-adapter/internal/solana/multisig.go#L509)）。若进程在 `MintTo` 期间崩溃，DB 里仍是 `approved`，重启后可被再次执行 → **双花铸造**。
2. 回滚逻辑（[multisig.go:490-493](services/chain-adapter/internal/solana/multisig.go#L490-L493)）只改内存状态，不持久化，同样在崩溃下丢失。
3. 由于真正的 `MintTo` 当前是返回错误的占位实现，这条路径**目前必然走回滚分支**——风险是潜伏的，等 Phase 4 接上真实 SDK 后才会引爆。

### 修复
执行应做成幂等：以 `proposalID` 为幂等键，"标记 executing + 记录意图"先在一个 DB 事务里落库，再上链；上链成功写 `executed_tx_id`。重启时对 `executing` 状态的提案先查链上是否已落地再决定重试，避免重复 mint。

---

## 9. 🟡 M-04（Medium）：mint 入账与多签投票彻底脱节

### 位置
[mint_service.go:286-309](services/mint/internal/service/mint_service.go#L286-L309)（自动批准 + 直接入账） vs [multisig.go](services/chain-adapter/internal/solana/multisig.go)（整套 2-of-3 治理）

### 问题
代码里存在两套独立的"铸造授权"叙事，但**从不交汇**：

- multisig.go 用大量篇幅实现了 2-of-3 投票治理，注释称"所有 mint/burn 操作需要 2-of-3 批准"（[multisig.go:98-99](services/chain-adapter/internal/solana/multisig.go#L98-L99)）。
- 但实际的铸造入账走的是 mint 服务的 `ConfirmDeposit`：风控阈值以下**全自动批准**，直接调 `ledgerService.MintCredit` 给用户贷记 vUSDC（[mint_service.go:286-304](services/mint/internal/service/mint_service.go#L286-L304)），**全程不经过 multisig**。
- grep 确认 `ProposeMint`/`ApproveProposal`/`ExecuteProposal` **在整个仓库里没有任何 HTTP handler 或服务层调用**，只有 multisig.go 自身和测试引用。这套投票机制是**孤儿代码**。

也就是说：投票治理写得很认真，却没接到任何真实的铸造决策点上。影子账本模式下"铸造"等于一条 `MintCredit` 记账，门槛仅是风控阈值，与 2-of-3 多签毫无关系。

### 定性
这是架构层面的 "AI 编程偏差"：模型按"稳定币该有多签治理"的常识生成了完整 multisig 模块，又按"充值即入账"的另一条常识生成了 mint 流程，两者各自完整、彼此不知道对方存在。**不是 bug，是集成缺失**——但它让所有关于"铸造受多签保护"的安全假设落空。

### 修复
明确边界：
- 若影子账本阶段确实不需要链上 mint，应**删除或明确标注 multisig 为未启用**，避免误以为有治理保护。
- 若需要多签门槛，则大额铸造（`RequireManualApprovalAboveMinor` 以上）应转入 `ProposeMint` → 2-of-3 → `ExecuteProposal` 流程，而不是现在的"超过阈值直接 fail"（[mint_service.go:277-284](services/mint/internal/service/mint_service.go#L277-L284)）。

---

## 10. 做得对的地方（避免以偏概全）

审查也确认了若干设计良好、不应误伤的部分：

- **充值入账幂等**：`ConfirmDeposit` 以 `deposit_tx_id` 为幂等键，SERIALIZABLE 事务 + `GetByDepositTxIDForUpdate` 锁，重复投递返回 nil（[mint_service.go:166-183](services/mint/internal/service/mint_service.go#L166-L183)）；008 迁移加了 `reserve_deposit_tx_id` 唯一索引兜底。
- **赎回余额检查**：`HasSufficientBalance` 在事务内用 `pg_advisory_xact_lock` 串行化同钱包并发，防双花（[ledger_service.go:243-261](services/ledger/internal/service/ledger_service.go#L243-L261)）。
- **储备覆盖闸门**：入账前 `CheckReserveCoverageForUpdate` 校验 `liability + new <= confirmed_reserve`（[mint_repository.go:391-410](services/mint/internal/repository/mint_repository.go#L391-L410)）——逻辑正确，只是上游金额可被 S-01 污染。
- **内部接口鉴权**：`InternalAPIKeyAuth` 用 `subtle.ConstantTimeCompare` 防时序侧信道，未配置时 fail-closed 返回 503（[internal_auth.go:17-42](services/mint/internal/middleware/internal_auth.go#L17-L42)）。
- **赎回状态机 + 失败释放**：`ProcessRedemption`/`ReleaseFunds` 的状态流转和 ledger 反向分录配对完整。

这些说明项目主体的事务一致性框架是扎实的，问题集中在"链上数据信任边界"与"被孤立/被错误实现的安全校验"。

---

## 11. AI 编程偏差模式总结

把上面的问题抽象出来，本项目的 AI 代码偏差呈现 4 个反复出现的模式：

1. **"定义了却不用"**——`USDCMainnetMint`/`USDCDevnetMint` 常量、`MintAddress` 字段、`ValidateBalance` 函数都被定义得很完整，却在关键路径上零引用或空赋值。AI 倾向于补齐"看起来该有"的声明，但不保证把它接到逻辑里。
2. **"注释承诺 > 代码实现"**——注释说"绑定 amount/dest/nonce 签名""所有 mint 需 2-of-3""校验借贷平衡"，实现都缩水了。审查时不能只读注释/函数名。
3. **"契约不对齐"**——公钥编码 base58 vs hex 在 4 个文件里各执一词；这是多段独立生成的代码拼接时的典型裂缝。
4. **"模板对、语义错"**——`ValidateBalance` 套用求和模板但加错字段；下溢检查照搬无符号减法没考虑边界。

**审查启示**：对 AI 生成的安全敏感代码，重点不在"有没有写校验"，而在"校验是否真的接到数据流上、是否真的能拒绝非法输入"。本次最危险的 S-01 和 H-01，表面上"该有的常量/校验都有"，恰恰是这类偏差最容易蒙混过关的地方。

---

## 12. 修复优先级建议

| 优先级 | 问题 | 理由 |
|--------|------|------|
| P0 立即 | S-01 mint 校验缺失 | 直接击穿储备锚定，假币铸真币 |
| P0 立即 | H-03 ValidateBalance | 记账核心不变式失效，且易被误当闸门 |
| P1 本迭代 | H-01 / H-02 多签签名与重放 | 若启用链上 mint 则为 Critical，当前因占位实现暂缓 |
| P1 本迭代 | M-01 确认数下溢 | 与 S-01 叠加放大伪造充值风险 |
| P2 规划 | M-02 / M-03 / M-04 | 随 Phase 4 真实上链前必须解决；M-04 需先定架构 |

> 注：`MintTo`/`Burn`/`DeployVUSDC` 当前均为返回 `Phase 4 ...` 错误的占位实现，链上铸造尚未真正启用。这意味着 H-01/H-02/M-03 等"链上路径"问题尚未在生产引爆，**现在是修复它们的最佳窗口**——一旦接上真实 Solana SDK，这些潜伏问题会同时转为可利用的高危。



# ANCF vUSDC Token-2022 Mint

按 [Solana 基金会官方文档](https://solana.com/zh/docs/tokens/basics/mint-tokens) 实现的 Rust 铸币程序。

## 前置依赖

```bash
# 安装 Rust
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh

# 安装 Solana CLI
sh -c "$(curl -sSfL https://release.anza.xyz/stable/install)"

# 验证
rustc --version   # >= 1.75
cargo --version   # >= 1.75
solana --version  # >= 1.18
```

## 创建钱包

```bash
# 生成新的 devnet 钱包
solana-keygen new --outfile payer.json

# 查看公钥地址
solana-keygen pubkey payer.json

# 配置 devnet
solana config set --url devnet

# 获取测试 SOL (devnet only)
solana airdrop 1 $(solana-keygen pubkey payer.json)

# 检查余额
solana balance $(solana-keygen pubkey payer.json)
```

## 编译

```bash
cd onchain/vusdc-mint
cargo build --release
```

## Dry-run 预览

```bash
cargo run --release -- \
  --rpc-url https://api.devnet.solana.com \
  --payer-keypair payer.json \
  --decimals 6 \
  --mint-amount 1000000000000 \
  --dry-run
```

## 执行铸币 (devnet)

```bash
cargo run --release -- \
  --rpc-url https://api.devnet.solana.com \
  --payer-keypair payer.json \
  --decimals 6 \
  --mint-amount 1000000000000
```

## 指定目标接收地址

```bash
cargo run --release -- \
  --rpc-url https://api.devnet.solana.com \
  --payer-keypair payer.json \
  --destination <YOUR_WALLET_ADDRESS> \
  --decimals 6 \
  --mint-amount 1000000000000
```

## 铸币后操作

```bash
# 1. 查看 token balance
spl-token balance <MINT_ADDRESS>

# 2. 在浏览器查看
open https://explorer.solana.com/address/<MINT_ADDRESS>?cluster=devnet

# 3. 将 mint authority 移交多签 (Phase 4)
spl-token authorize <MINT_ADDRESS> mint <MULTISIG_PDA> \
  --owner payer.json \
  --program-id TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb

# 4. 放弃 freeze authority (可选)
spl-token authorize <MINT_ADDRESS> freeze --disable \
  --owner payer.json \
  --program-id TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb
```

## 安全提醒

- `payer.json` 包含私钥，绝对不要提交到 git
- 生产环境使用 KMS/HSM，不要用本地密钥文件
- devnet 可以随意测试，主网部署前需审计

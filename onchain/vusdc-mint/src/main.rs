//! ANCF vUSDC Token Mint — Solana Token-2022
//!
//! 按照 Solana 基金会官方文档实现:
//!   https://solana.com/zh/docs/tokens/basics/mint-tokens
//!
//! 流程:
//!   1. 加载/生成 payer 密钥对
//!   2. 检查余额 (devnet airdrop 如需要)
//!   3. 创建 Token-2022 Mint Account (含 compute budget 指令)
//!   4. 创建 Associated Token Account (ATA)
//!   5. Mint Tokens 到 ATA
//!   6. 链上验证
//!
//! 安全:
//!   - 密钥对文件永不提交到 git
//!   - 生产环境使用 KMS/HSM 签名
//!   - devnet 模式默认开启
//!   - 交易含优先级费用防拥堵与计算预算
//!   - 交易确认重试逻辑防网络波动
//!   - Token-2022 正确使用 ATA with program ID

use anyhow::{Context, Result};
use clap::{Parser, ValueEnum};
use solana_client::rpc_client::RpcClient;
use solana_sdk::{
    commitment_config::CommitmentConfig,
    compute_budget::ComputeBudgetInstruction,
    instruction::Instruction,
    native_token::LAMPORTS_PER_SOL,
    pubkey::Pubkey,
    signature::{Keypair, Signature, Signer},
    system_instruction,
    transaction::Transaction,
};
use spl_associated_token_account::{
    get_associated_token_address_with_program_id,
    instruction::create_associated_token_account_idempotent,
};
use spl_token_2022::{
    instruction::{initialize_mint2, mint_to},
    ID as TOKEN_2022_ID,
};

// ---------------------------------------------------------------------------
// Cluster
// ---------------------------------------------------------------------------

/// Solana cluster target
#[derive(ValueEnum, Clone, Debug)]
enum Cluster {
    /// Devnet (default)
    Devnet,
    /// Testnet
    Testnet,
    /// Mainnet Beta
    Mainnet,
}

impl Cluster {
    fn rpc_url(&self) -> &str {
        match self {
            Cluster::Devnet => "https://api.devnet.solana.com",
            Cluster::Testnet => "https://api.testnet.solana.com",
            Cluster::Mainnet => "https://api.mainnet-beta.solana.com",
        }
    }
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

#[derive(Parser, Debug)]
#[command(name = "vusdc-mint")]
#[command(about = "Deploy and mint ANCF vUSDC Token-2022 on Solana", version = "1.0")]
struct Cli {
    /// Solana cluster: devnet, testnet, mainnet
    #[arg(long, default_value = "devnet")]
    cluster: Cluster,

    /// Custom RPC URL (overrides cluster default)
    #[arg(long)]
    rpc_url: Option<String>,

    /// Payer keypair file path (JSON array [u8; 64] or base58)
    #[arg(long, default_value = "payer.json")]
    payer_keypair: String,

    /// vUSDC token decimals (default: 6, matching USDC)
    #[arg(long, default_value_t = 6)]
    decimals: u8,

    /// Initial mint amount in token native units (e.g. 1000000 = 1.0 vUSDC with 6 decimals)
    #[arg(long, default_value_t = 1_000_000_000_000)] // 1M vUSDC
    mint_amount: u64,

    /// Destination wallet that receives minted tokens
    #[arg(long, default_value = "Giqt4TrXHzkPBYSD4Rs9K9VB6BVqznsWnmyPgVJKdhDw")]
    destination: String,

    /// Skip airdrop check (use if already funded)
    #[arg(long, default_value_t = false)]
    skip_airdrop: bool,

    /// Dry-run: print what would happen without submitting
    #[arg(long, default_value_t = false)]
    dry_run: bool,

    /// Freeze authority (default: none)
    #[arg(long, default_value = "")]
    freeze_authority: String,

    /// Compute unit limit per transaction (default: 300,000)
    #[arg(long, default_value_t = 300_000)]
    compute_unit_limit: u32,

    /// Priority fee in micro-lamports per compute unit (default: 1,000)
    #[arg(long, default_value_t = 1_000)]
    priority_fee: u64,
}

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

/// Token-2022 mint account 空间大小 (82 bytes, 不含扩展)
const MINT_SIZE: u64 = 82;

/// 创建 mint account 所需的 rent 豁免 (约 0.00146 SOL @ devnet)
const MINT_RENT_LAMPORTS: u64 = 1_461_600;

/// ATA 创建费用约 0.002 SOL
const ATA_RENT_LAMPORTS: u64 = 2_039_280;

/// 单笔交易费 (保守估计, 0.00005 SOL)
const TX_FEE_LAMPORTS: u64 = 50_000;

/// 总预算: 3 笔交易 * (fee + 优先费) + mint rent + ATA rent
///        ≈ 3 * 50000 + 1461600 + 2039280 ≈ 3,650,880 lamports ≈ 0.0037 SOL
/// 保守 0.02 SOL
const MIN_BALANCE_LAMPORTS: u64 = 20_000_000; // 0.02 SOL

/// 最大交易确认重试次数
const MAX_CONFIRM_RETRIES: u32 = 5;

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

fn main() -> Result<()> {
    let cli = Cli::parse();

    // Resolve RPC URL: --rpc-url overrides --cluster default
    let rpc_url = cli
        .rpc_url
        .clone()
        .unwrap_or_else(|| cli.cluster.rpc_url().to_string());

    println!("╔══════════════════════════════════════════════╗");
    println!("║  ANCF vUSDC Token-2022 Mint                  ║");
    println!("╠══════════════════════════════════════════════╣");
    println!("║  Cluster:  {:<36}║", format!("{:?}", cli.cluster));
    println!("║  RPC:      {:<36}║", rpc_url);
    println!("║  Decimals: {:<35}║", cli.decimals);
    println!(
        "║  Amount:   {:<35}║",
        format_amount(cli.mint_amount, cli.decimals)
    );
    println!(
        "║  Priority: {:<2} µlamports/CU ({} SOL/tx est.)║",
        cli.priority_fee,
        format!(
            "{:.6}",
            (cli.compute_unit_limit as u64 * cli.priority_fee) as f64 / LAMPORTS_PER_SOL as f64
        )
    );
    println!("╚══════════════════════════════════════════════╝");
    println!();

    // ── Step 1: Connect ──
    let rpc = RpcClient::new_with_commitment(rpc_url.clone(), CommitmentConfig::confirmed());
    println!("[1/6] Connected to {}", rpc_url);

    // Health check
    match rpc.get_version() {
        Ok(v) => println!("      RPC version: {:?}", v),
        Err(e) => eprintln!("      ⚠ RPC health check failed: {e}"),
    }

    // ── Step 2: Load payer ──
    let payer = load_keypair(&cli.payer_keypair)?;
    let payer_pubkey = payer.pubkey();
    println!("[2/6] Payer: {}", payer_pubkey);

    // Check balance and optionally airdrop
    let balance = rpc.get_balance(&payer_pubkey)?;
    println!(
        "      Balance: {} SOL",
        balance as f64 / LAMPORTS_PER_SOL as f64
    );

    if !cli.skip_airdrop && balance < MIN_BALANCE_LAMPORTS {
        let needed = MIN_BALANCE_LAMPORTS.saturating_sub(balance);
        let airdrop_lamports = LAMPORTS_PER_SOL.max(needed); // at least 1 SOL
        println!(
            "      Requesting airdrop of {} SOL...",
            airdrop_lamports as f64 / LAMPORTS_PER_SOL as f64
        );
        let sig = rpc.request_airdrop(&payer_pubkey, airdrop_lamports)?;
        confirm_with_retry(&rpc, &sig, MAX_CONFIRM_RETRIES, "airdrop")?;
        let new_balance = rpc.get_balance(&payer_pubkey)?;
        println!(
            "      New balance: {} SOL (tx: {}...)",
            new_balance as f64 / LAMPORTS_PER_SOL as f64,
            &sig.to_string()[..16]
        );
    }

    if balance < MIN_BALANCE_LAMPORTS && cli.skip_airdrop {
        anyhow::bail!(
            "Insufficient balance: {} SOL (need {} SOL). Remove --skip-airdrop or fund wallet.",
            balance as f64 / LAMPORTS_PER_SOL as f64,
            MIN_BALANCE_LAMPORTS as f64 / LAMPORTS_PER_SOL as f64,
        );
    }

    // ═══ Compute budget instructions (shared across all txs) ═══
    let compute_budget_ixs: Vec<Instruction> = vec![
        ComputeBudgetInstruction::set_compute_unit_limit(cli.compute_unit_limit),
        ComputeBudgetInstruction::set_compute_unit_price(cli.priority_fee),
    ];

    // ── Step 3: Create mint account ──
    // Uses system_instruction::create_account + initialize_mint2 per official docs
    let mint_keypair = Keypair::new();
    let mint_pubkey = mint_keypair.pubkey();
    println!("[3/6] Creating Token-2022 mint account...");
    println!("      Mint: {}", mint_pubkey);

    let mint_authority = payer_pubkey;
    let freeze_authority: Option<Pubkey> = if cli.freeze_authority.is_empty() {
        None
    } else {
        Some(cli.freeze_authority.parse()?)
    };

    if cli.dry_run {
        println!("      [DRY RUN] Would create mint: {}", mint_pubkey);
    } else {
        let create_account_ix = system_instruction::create_account(
            &payer_pubkey,
            &mint_pubkey,
            MINT_RENT_LAMPORTS,
            MINT_SIZE,
            &TOKEN_2022_ID,
        );

        let init_mint_ix = initialize_mint2(
            &TOKEN_2022_ID,
            &mint_pubkey,
            &mint_authority,
            freeze_authority.as_ref(),
            cli.decimals,
        )?;

        // Assemble instructions: compute budget + create account + init mint
        let all_ixs: Vec<Instruction> = compute_budget_ixs
            .iter()
            .cloned()
            .chain(std::iter::once(create_account_ix))
            .chain(std::iter::once(init_mint_ix))
            .collect();

        let sig = send_and_confirm_with_retry(
            &rpc,
            &payer,
            &all_ixs,
            &[&payer, &mint_keypair],
            MAX_CONFIRM_RETRIES,
        )?;
        println!("      Mint created: tx={}", sig);
    }

    // ── Step 4: Create ATA ──
    let destination: Pubkey = if cli.destination.is_empty() {
        payer_pubkey
    } else {
        cli.destination.parse()?
    };

    // Token-2022 必须使用 get_associated_token_address_with_program_id
    let ata = get_associated_token_address_with_program_id(
        &destination,
        &mint_pubkey,
        &TOKEN_2022_ID,
    );
    println!("[4/6] Associated Token Account...");
    println!("      Owner: {}", destination);
    println!("      ATA:   {}", ata);

    let ata_exists = rpc.get_account(&ata).is_ok();

    if !ata_exists && !cli.dry_run {
        // 使用 idempotent 版本, 即使 ATA 已存在也不会报错
        let create_ata_ix = create_associated_token_account_idempotent(
            &payer_pubkey,
            &destination,
            &mint_pubkey,
            &TOKEN_2022_ID,
        );

        let all_ixs: Vec<Instruction> = compute_budget_ixs
            .iter()
            .cloned()
            .chain(std::iter::once(create_ata_ix))
            .collect();

        let sig = send_and_confirm_with_retry(
            &rpc,
            &payer,
            &all_ixs,
            &[&payer],
            MAX_CONFIRM_RETRIES,
        )?;
        println!("      ATA created: tx={}", sig);
    } else if ata_exists {
        println!("      ATA already exists (reusing)");
    } else {
        println!("      [DRY RUN] Would create ATA: {}", ata);
    }

    // ── Step 5: Mint tokens ──
    println!("[5/6] Minting vUSDC tokens...");
    println!(
        "      Amount: {} ({} vUSDC)",
        cli.mint_amount,
        format_amount(cli.mint_amount, cli.decimals)
    );

    if cli.dry_run {
        println!(
            "      [DRY RUN] Would mint {} tokens to {}",
            cli.mint_amount, ata
        );
    } else {
        let mint_to_ix = mint_to(
            &TOKEN_2022_ID,
            &mint_pubkey,
            &ata,
            &payer_pubkey, // mint authority = payer
            &[],            // multi_signers (none for single authority)
            cli.mint_amount,
        )?;

        let all_ixs: Vec<Instruction> = compute_budget_ixs
            .iter()
            .cloned()
            .chain(std::iter::once(mint_to_ix))
            .collect();

        let sig = send_and_confirm_with_retry(
            &rpc,
            &payer,
            &all_ixs,
            &[&payer],
            MAX_CONFIRM_RETRIES,
        )?;
        println!("      Tokens minted: tx={}", sig);
    }

    // ── Step 6: Verify ──
    println!("[6/6] Verification...");
    if !cli.dry_run {
        let token_balance = rpc.get_token_account_balance(&ata)?;
        println!(
            "      ATA balance: {} {}",
            token_balance.ui_amount_string, "vUSDC"
        );

        let mint_supply = rpc.get_token_supply(&mint_pubkey)?;
        println!(
            "      Total supply: {} {}",
            mint_supply.ui_amount_string, "vUSDC"
        );
    } else {
        println!("      [DRY RUN] Verification skipped");
    }

    // ── Output manifest ──
    let network = cli
        .rpc_url
        .as_deref()
        .map(|url| {
            if url.contains("devnet") {
                "devnet"
            } else if url.contains("testnet") {
                "testnet"
            } else if url.contains("mainnet") {
                "mainnet-beta"
            } else {
                "custom"
            }
        })
        .unwrap_or_else(|| match cli.cluster {
            Cluster::Devnet => "devnet",
            Cluster::Testnet => "testnet",
            Cluster::Mainnet => "mainnet-beta",
        });

    println!();
    println!("╔══════════════════════════════════════════════╗");
    println!("║  Deployment Manifest (save this!)            ║");
    println!("╠══════════════════════════════════════════════╣");
    println!("║  network:          {:<26}║", network);
    println!("║  mint_address:     {:<26}║", mint_pubkey);
    println!("║  mint_authority:   {:<26}║", mint_authority);
    println!(
        "║  freeze_authority: {:<26}║",
        freeze_authority.map_or("none".to_string(), |p| p.to_string())
    );
    println!("║  decimals:         {:<26}║", cli.decimals);
    println!("║  payer:            {:<26}║", payer_pubkey);
    println!(
        "║  mint_amount:      {:<26}║",
        format_amount(cli.mint_amount, cli.decimals)
    );
    if !cli.dry_run {
        println!("╚══════════════════════════════════════════════╝");
        println!();
        println!("✅ vUSDC Token-2022 mint deployed successfully!");
        println!();
        println!("Next steps:");
        println!(
            "  1. Transfer mint authority to 2-of-3 multisig:"
        );
        println!(
            "     ./scripts/create-multisig.sh {}",
            mint_pubkey
        );
        println!(
            "  2. Verify on explorer: https://explorer.solana.com/address/{}?cluster={}",
            mint_pubkey, network
        );
        println!(
            "  3. Run verification script: ./scripts/verify-mint.sh {}",
            mint_pubkey
        );
    } else {
        println!("╚══════════════════════════════════════════════╝");
        println!();
        println!("🔍 DRY RUN complete. To execute, remove --dry-run");
    }

    Ok(())
}

// ---------------------------------------------------------------------------
// Transaction helpers
// ---------------------------------------------------------------------------

/// Send a signed transaction and confirm it with exponential-backoff retries.
///
/// Each retry re-fetches the latest blockhash and re-signs, so stale blockhash
/// timeouts are handled correctly.
fn send_and_confirm_with_retry(
    rpc: &RpcClient,
    payer: &Keypair,
    instructions: &[Instruction],
    signers: &[&Keypair],
    max_retries: u32,
) -> Result<Signature> {
    let mut last_err: Option<anyhow::Error> = None;

    for attempt in 1..=max_retries {
        // Fresh blockhash per attempt (prevents stale blockhash issues)
        let blockhash = rpc
            .get_latest_blockhash()
            .with_context(|| format!("Failed to get blockhash (attempt {attempt})"))?;

        let mut tx = Transaction::new_with_payer(instructions, Some(&payer.pubkey()));
        tx.sign(signers, blockhash);

        match rpc.send_and_confirm_transaction(&tx) {
            Ok(sig) => {
                if attempt > 1 {
                    println!("      ✓ Confirmed on attempt {attempt}");
                }
                return Ok(sig);
            }
            Err(e) => {
                if attempt < max_retries {
                    let wait_ms = 500 * 2u64.pow(attempt - 1);
                    let wait = std::time::Duration::from_millis(wait_ms);
                    eprintln!(
                        "      ⚠ Attempt {}/{} failed, retrying in {:?}: {}",
                        attempt,
                        max_retries,
                        wait,
                        truncate_err(&e, 100)
                    );
                    std::thread::sleep(wait);
                }
                last_err = Some(e.into());
            }
        }
    }

    Err(last_err
        .unwrap_or_else(|| anyhow::anyhow!("Transaction failed after {max_retries} attempts")))
        .with_context(|| format!("All {max_retries} send attempts exhausted"))
}

/// Confirm a previously-submitted transaction signature with retries.
///
/// Used for operations where the tx is submitted externally (e.g. airdrop).
fn confirm_with_retry(
    rpc: &RpcClient,
    sig: &Signature,
    max_retries: u32,
    label: &str,
) -> Result<()> {
    for attempt in 1..=max_retries {
        match rpc.confirm_transaction(sig) {
            Ok(true) => return Ok(()),
            Ok(false) => {
                // Transaction not yet confirmed — poll again
            }
            Err(e) => {
                eprintln!(
                    "      ⚠ {label} confirmation attempt {attempt}/{max_retries}: {}",
                    truncate_err(&e, 80)
                );
            }
        }
        if attempt < max_retries {
            let wait_ms = 500 * 2u64.pow(attempt - 1);
            std::thread::sleep(std::time::Duration::from_millis(wait_ms));
        }
    }
    anyhow::bail!(
        "{label} confirmation failed after {max_retries} attempts (sig: {})",
        sig
    )
}

// ---------------------------------------------------------------------------
// Key helpers
// ---------------------------------------------------------------------------

/// 从 JSON 文件加载密钥对 (Solana 标准格式: [u8; 64])
///
/// 支持两种格式:
///   1. JSON array: `[1,2,3,...,64]`  (solana-keygen new 输出)
///   2. Base58 编码: 88-character string (Phantom/Wallet 导出)
fn load_keypair(path: &str) -> Result<Keypair> {
    let bytes = std::fs::read_to_string(path)
        .with_context(|| format!("无法读取密钥文件: {path}. 请先运行: solana-keygen new --outfile {path} --no-bip39-passphrase"))?;

    // 格式 1: JSON array [1,2,3,...64] (solana-keygen 输出)
    if let Ok(arr) = serde_json::from_str::<Vec<u8>>(&bytes) {
        if arr.len() == 64 {
            return Ok(Keypair::from_bytes(&arr)?);
        }
        anyhow::bail!(
            "JSON array 长度为 {} (期望 64 字节)。请检查密钥文件是否完整。",
            arr.len()
        );
    }

    // 格式 2: Base58 编码 (phantom/wallet 导出)
    if let Ok(decoded) = bs58::decode(bytes.trim()).into_vec() {
        if decoded.len() == 64 {
            return Ok(Keypair::from_bytes(&decoded)?);
        }
        anyhow::bail!(
            "Base58 解码得到 {} 字节 (期望 64)。请确认密钥编码正确。",
            decoded.len()
        );
    }

    anyhow::bail!(
        "密钥文件格式不支持。请使用 solana-keygen new 生成 (JSON array [u8; 64]) 或提供 base58 编码的 64 字节密钥。"
    )
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

/// 格式化 token amount 为人类可读
fn format_amount(amount: u64, decimals: u8) -> String {
    let divisor = 10u64.pow(decimals as u32) as f64;
    if decimals == 0 {
        return format!("{} vUSDC", amount);
    }
    format!("{:.6} vUSDC", amount as f64 / divisor)
}

/// Truncate error message for compact log output
fn truncate_err(e: &dyn std::error::Error, max_len: usize) -> String {
    let s = e.to_string();
    if s.len() <= max_len {
        s
    } else {
        format!("{}...", &s[..max_len.saturating_sub(3)])
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_format_amount() {
        assert_eq!(format_amount(1_000_000, 6), "1.000000 vUSDC");
        assert_eq!(format_amount(2_450_000, 6), "2.450000 vUSDC");
        assert_eq!(format_amount(0, 6), "0.000000 vUSDC");
        assert_eq!(format_amount(1_000_000_000_000, 6), "1000000.000000 vUSDC");
    }

    #[test]
    fn test_format_amount_zero_decimals() {
        assert_eq!(format_amount(100, 0), "100 vUSDC");
    }

    #[test]
    fn test_truncate_err() {
        let e = anyhow::anyhow!("short error");
        assert_eq!(truncate_err(&e, 50), "short error");

        let long_e = anyhow::anyhow!("this is a very long error message that should be truncated at some point");
        let truncated = truncate_err(&long_e, 20);
        assert!(truncated.len() <= 20);
        assert!(truncated.ends_with("..."));
    }
}

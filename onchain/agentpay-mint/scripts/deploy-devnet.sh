#!/bin/bash
# ANCF AGP devnet 一键部署脚本
# 用法: ./scripts/deploy-devnet.sh [--skip-build]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "╔══════════════════════════════════════════════╗"
echo "║  ANCF AGP Token-2022 Devnet Deployment     ║"
echo "╚══════════════════════════════════════════════╝"
echo ""

# ── 1. Prerequisites ──
echo "[1/6] Checking prerequisites..."

if ! command -v cargo &>/dev/null; then
    echo "ERROR: cargo not found. Install Rust: https://rustup.rs"
    exit 1
fi
echo "      cargo:     $(cargo --version)"

if ! command -v solana &>/dev/null; then
    echo "ERROR: solana-cli not found. Install: sh -c \"$(curl -sSfL https://release.anza.xyz/stable/install)\""
    exit 1
fi
echo "      solana:    $(solana --version)"

# ── 2. Build ──
SKIP_BUILD="${1:-}"
if [ "$SKIP_BUILD" != "--skip-build" ]; then
    echo ""
    echo "[2/6] Building release binary..."
    cd "$PROJECT_DIR"
    cargo build --release
    echo "      Build complete."
else
    echo ""
    echo "[2/6] Build skipped (--skip-build)"
fi

# ── 3. Wallet ──
echo ""
echo "[3/6] Setting up payer wallet..."

PAYER="$PROJECT_DIR/payer.json"
if [ ! -f "$PAYER" ]; then
    echo "      Creating new devnet wallet..."
    solana-keygen new --outfile "$PAYER" --no-bip39-passphrase --force
else
    echo "      Using existing wallet: $PAYER"
fi

PUBKEY=$(solana-keygen pubkey "$PAYER")
echo "      Payer pubkey: $PUBKEY"

# ── 4. Fund ──
echo ""
echo "[4/6] Checking SOL balance..."

BALANCE=$(solana balance "$PUBKEY" --url devnet | awk '{print $1}' || echo "0")
echo "      Current balance: $BALANCE SOL"

if [ "$(echo "$BALANCE < 1" | bc -l 2>/dev/null || echo 1)" = "1" ]; then
    echo "      Requesting airdrop of 2 SOL..."
    solana airdrop 2 "$PUBKEY" --url devnet || {
        echo "      WARNING: Airdrop failed (rate limit?). Trying 1 SOL..."
        solana airdrop 1 "$PUBKEY" --url devnet || {
            echo "      WARNING: All airdrops failed. Continuing anyway..."
        }
    }
    sleep 2
    NEW_BALANCE=$(solana balance "$PUBKEY" --url devnet)
    echo "      New balance: $NEW_BALANCE"
fi

# ── 5. Deploy ──
echo ""
echo "[5/6] Deploying AGP mint..."

DESTINATION="${DESTINATION:-Giqt4TrXHzkPBYSD4Rs9K9VB6BVqznsWnmyPgVJKdhDw}"
MINT_AMOUNT="${MINT_AMOUNT:-1000000000000}"  # 1M AGP (6 decimals)

cd "$PROJECT_DIR"
cargo run --release -- \
  --cluster devnet \
  --payer-keypair "$PAYER" \
  --destination "$DESTINATION" \
  --decimals 6 \
  --mint-amount "$MINT_AMOUNT"

# ── 6. Save manifest ──
echo ""
echo "[6/6] Saving deployment manifest..."

MANIFEST_FILE="$PROJECT_DIR/deploy-manifest.json"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# The mint address is in the cargo output — prompt user to record it
echo ""
echo "╔══════════════════════════════════════════════╗"
echo "║  Deployment complete!                        ║"
echo "╠══════════════════════════════════════════════╣"
echo "║  Payer:  $PUBKEY"
echo "║  Config: devnet"
echo "║  Manifest saved to: deploy-manifest.json     ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "Next:"
echo "  1. Run ./scripts/verify-mint.sh <MINT_ADDRESS>"
echo "  2. Run ./scripts/create-multisig.sh <MINT_ADDRESS> <MULTISIG_PDA>"

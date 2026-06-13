#!/bin/bash
# ANCF AGP — Transfer mint authority to 2-of-3 multisig
# 用法: ./scripts/create-multisig.sh <MINT_ADDRESS> <MULTISIG_PDA>
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

if [ $# -lt 2 ]; then
    echo "Usage: $0 <MINT_ADDRESS> <MULTISIG_PDA>"
    echo ""
    echo "Transfers mint authority of the AGP Token-2022 mint"
    echo "from the payer wallet to a 2-of-3 multisig PDA."
    echo ""
    echo "Example:"
    echo "  $0 4zMMC...XyzW 3xABC...DefG"
    echo ""
    echo "Prerequisites:"
    echo "  1. payer.json exists in project root"
    echo "  2. The multisig PDA has been created on-chain"
    echo "  3. 2-of-3 signers are configured"
    exit 1
fi

MINT_ADDRESS="$1"
MULTISIG_PDA="$2"
PAYER="$PROJECT_DIR/payer.json"
CLUSTER="${CLUSTER:-devnet}"

echo "╔══════════════════════════════════════════════╗"
echo "║  ANCF AGP — Mint Authority Transfer        ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "  Cluster:     $CLUSTER"
echo "  Mint:        $MINT_ADDRESS"
echo "  New Auth:    $MULTISIG_PDA"
echo "  Payer:       $(solana-keygen pubkey "$PAYER")"
echo ""

if [ ! -f "$PAYER" ]; then
    echo "ERROR: $PAYER not found. Run deploy-devnet.sh first."
    exit 1
fi

# ── 1. Verify current authority ──
echo "[1/3] Verifying current mint authority..."
CURRENT_AUTH=$(spl-token display "$MINT_ADDRESS" \
    --url "$CLUSTER" \
    --program-id TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb 2>/dev/null \
    | grep -i "mint authority" | awk '{print $NF}' || echo "unknown")

echo "      Current: $CURRENT_AUTH"
PAYER_PUBKEY=$(solana-keygen pubkey "$PAYER")

if [ "$CURRENT_AUTH" != "$PAYER_PUBKEY" ]; then
    echo "      WARNING: Payer ($PAYER_PUBKEY) does not match current mint authority ($CURRENT_AUTH)"
    read -rp "      Continue? (y/N) " CONFIRM
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
        echo "      Aborted."
        exit 1
    fi
fi

# ── 2. Transfer authority ──
echo ""
echo "[2/3] Transferring mint authority..."

spl-token authorize "$MINT_ADDRESS" mint "$MULTISIG_PDA" \
    --owner "$PAYER" \
    --program-id TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb \
    --url "$CLUSTER"

echo "      Authority transfer submitted."

# Wait for confirmation
sleep 3

# ── 3. Verify transfer ──
echo ""
echo "[3/3] Verifying transfer..."

spl-token display "$MINT_ADDRESS" \
    --url "$CLUSTER" \
    --program-id TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb

echo ""
echo "╔══════════════════════════════════════════════╗"
echo "║  Mint authority transferred to multisig!      ║"
echo "╠══════════════════════════════════════════════╣"
echo "║  Mint:       $MINT_ADDRESS"
echo "║  New Auth:   $MULTISIG_PDA"
echo "║  Old Payer:  $PAYER_PUBKEY (no longer has mint authority)"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "⚠  Keep payer.json backed up securely in case of multisig key loss."

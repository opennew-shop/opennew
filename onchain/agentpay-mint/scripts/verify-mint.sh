#!/bin/bash
# ANCF AGP — On-chain mint verification script
# 用法: ./scripts/verify-mint.sh <MINT_ADDRESS> [--cluster devnet|testnet|mainnet]
set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $0 <MINT_ADDRESS> [--cluster <devnet|testnet|mainnet>]"
    echo ""
    echo "Verifies a deployed AGP Token-2022 mint:"
    echo "  - Token metadata (name, symbol, decimals, authorities)"
    echo "  - Total supply"
    echo "  - All token accounts and balances"
    echo ""
    echo "Example:"
    echo "  $0 4zMMC9Urt1AW5TJFxLxV3AQnWFox4n2xMiCVzvXyzWq"
    echo "  $0 4zMMC... --cluster mainnet"
    exit 1
fi

MINT_ADDRESS="$1"
CLUSTER="devnet"

# Parse optional --cluster flag
shift
while [ $# -gt 0 ]; do
    case "$1" in
        --cluster)
            CLUSTER="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

case "$CLUSTER" in
    devnet|testnet|mainnet|mainnet-beta) ;;
    *)
        echo "ERROR: Invalid cluster '$CLUSTER'. Use: devnet, testnet, mainnet"
        exit 1
        ;;
esac

# Normalize mainnet-beta -> mainnet for URL
CLUSTER_URL="$CLUSTER"
if [ "$CLUSTER" = "mainnet" ]; then
    CLUSTER_URL="mainnet-beta"
fi

PROGRAM_ID="TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"

echo "╔══════════════════════════════════════════════╗"
echo "║  ANCF AGP — Mint Verification              ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "  Cluster:     $CLUSTER"
echo "  Mint:        $MINT_ADDRESS"
echo "  Program:     Token-2022"
echo ""

# ── 1. Token metadata ──
echo "[1/4] Token-2022 metadata..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
spl-token display "$MINT_ADDRESS" \
    --url "$CLUSTER" \
    --program-id "$PROGRAM_ID" 2>&1 || {
    echo "  ERROR: Could not fetch token metadata."
    echo "  Check that the mint address is correct and exists on $CLUSTER."
    exit 1
}
echo ""

# ── 2. Total supply ──
echo "[2/4] Total supply..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
SUPPLY=$(spl-token supply "$MINT_ADDRESS" --url "$CLUSTER" --program-id "$PROGRAM_ID" 2>&1)
echo "  $SUPPLY"
echo ""

# ── 3. Token accounts ──
echo "[3/4] Token accounts (all holders)..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
spl-token accounts "$MINT_ADDRESS" --url "$CLUSTER" --program-id "$PROGRAM_ID" 2>&1 || {
    echo "  (No token accounts found or query failed)"
}
echo ""

# ── 4. Explorer link ──
echo "[4/4] Explorer link..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [ "$CLUSTER" = "mainnet" ] || [ "$CLUSTER" = "mainnet-beta" ]; then
    echo "  https://explorer.solana.com/address/$MINT_ADDRESS"
else
    echo "  https://explorer.solana.com/address/$MINT_ADDRESS?cluster=$CLUSTER"
fi
echo ""

# ── Summary ──
echo "╔══════════════════════════════════════════════╗"
echo "║  Verification complete                       ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "  If any checks failed:"
echo "    1. Confirm you are on the correct cluster (--cluster flag)"
echo "    2. Verify the mint address: solana address"
echo "    3. Check RPC connectivity: solana cluster-version --url $CLUSTER"

#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

pass() {
	printf 'PASS %s\n' "$1"
}

check_rg() {
	local desc="$1"
	local pattern="$2"
	local path="$3"
	if rg -n "$pattern" "$path" >/dev/null; then
		pass "$desc"
	else
		printf 'FAIL %s\n' "$desc"
		fail=1
	fi
}

check_absent() {
	local desc="$1"
	local pattern="$2"
	local path="$3"
	if rg -n "$pattern" "$path" >/dev/null; then
		printf 'FAIL %s\n' "$desc"
		rg -n "$pattern" "$path"
		fail=1
	else
		pass "$desc"
	fi
}

check_rg "deposit event carries mint address" 'MintAddress[[:space:]]+string[[:space:]]+`json:"mint_address"`' services/chain-adapter/internal/model/chain.go
check_rg "deposit event carries measured confirmations" 'Confirmations[[:space:]]+int[[:space:]]+`json:"confirmations"`' services/chain-adapter/internal/model/chain.go
check_rg "solana watcher rejects non-whitelisted token mints" 'postBal\.Mint != expectedMint' services/chain-adapter/internal/solana/deposit_watcher.go
check_rg "solana watcher writes observed mint into proof" 'MintAddress:[[:space:]]+postBal\.Mint' services/chain-adapter/internal/solana/deposit_watcher.go
check_rg "solana watcher computes actual confirmations before persistence" 'event\.Confirmations = int\(currentSlot - uint64\(event\.BlockNumber\)\)' services/chain-adapter/internal/solana/deposit_watcher.go
check_rg "chain-adapter entrypoint uses live solana watcher" 'solanawatcher\.NewSolanaDepositWatcher' services/chain-adapter/cmd/main.go
check_rg "chain-adapter entrypoint injects mint whitelist" 'SetAssetMints' services/chain-adapter/cmd/main.go
check_rg "mint proof model carries mint address" 'MintAddress[[:space:]]+string[[:space:]]+`json:"mint_address"`' services/mint/internal/model/mint.go
check_rg "mint service requires asset mint configuration" 'asset\.MintAddress == nil \|\| \*asset\.MintAddress == ""' services/mint/internal/service/mint_service.go
check_rg "mint service compares proof mint to asset mint" 'proof\.MintAddress != \*asset\.MintAddress' services/mint/internal/service/mint_service.go
check_rg "initial seed configures Solana USDC mint" 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v' services/migrations/001_init.sql
check_rg "existing DB migration configures Solana USDC mint" 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v' services/migrations/009_solana_mint_proof.sql

check_absent "multisig no longer hex-decodes signer public keys" 'hex\.DecodeString\(approver\.PublicKey\)' services/chain-adapter/internal/solana/multisig.go
check_absent "multisig no longer verifies signatures over a prehashed message" 'ed25519\.Verify\(.*sigMsgHash' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig decodes Solana base58 public keys" 'decodeBase58Fixed\(approver\.PublicKey, ed25519\.PublicKeySize\)' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig canonical approval binds destination" 'proposal\.DestAddress' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig canonical approval binds mint" 'proposal\.MintAddress' services/chain-adapter/internal/solana/multisig.go
check_rg "mint proposals derive IDs with configured mint" 'deriveProposalID\(ProposalActionMint, amount, destAddress, m\.config\.MintAddress, nonce\)' services/chain-adapter/internal/solana/multisig.go
check_rg "mint proposals persist configured mint" 'MintAddress:[[:space:]]+m\.config\.MintAddress' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig DB load failure is fail-fast" 'return nil, fmt\.Errorf\("multisig: load proposals from DB' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig has executing state" 'ProposalStatusExecuting' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig persists executing state before chain call" 'proposal\.Status = ProposalStatusExecuting' services/chain-adapter/internal/solana/multisig.go
check_rg "multisig migration allows executing state" 'executing' services/migrations/010_multisig_hardening.sql

check_rg "ledger rejects empty entry sets" 'len\(entries\) == 0' services/ledger/internal/model/ledger.go
check_rg "ledger rejects same debit and credit account" 'e\.DebitAccount == e\.CreditAccount' services/ledger/internal/model/ledger.go
check_rg "ledger validates known account names" 'ValidAccount\(e\.DebitAccount\)' services/ledger/internal/model/ledger.go
check_rg "ledger service validates before posting" 'postValidated' services/ledger/internal/service/ledger_service.go
check_rg "provisioning direct ledger writes validate settle entries" 'ValidateBalance\(settleEntries\)' services/provisioning/internal/service/provisioning_service.go
check_rg "provisioning direct ledger writes validate refund entries" 'ValidateBalance\(refundEntries\)' services/provisioning/internal/service/provisioning_service.go

if [ "$fail" -ne 0 ]; then
	printf '\nSOL security verification failed.\n'
	exit 1
fi

printf '\nSOL security verification passed.\n'

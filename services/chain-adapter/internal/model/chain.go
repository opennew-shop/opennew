// Package model 定义 chain-adapter（Solana 链适配器）服务的核心领域模型：
// 区块链网络枚举、链上交易记录（chain_txs）、充值事件（DepositEvent）
// 以及平台储备账户（reserve_accounts），供各层共享使用。
package model

import (
	"encoding/json"
	"time"
)

// Network identifies a blockchain network supported by the platform.
type Network string

const (
	// NetworkSolanaMainnet is the Solana mainnet-beta network.
	NetworkSolanaMainnet Network = "solana-mainnet"
	// NetworkSonicL2 is the Sonic L2 (Solana virtual machine on Ethereum) network.
	NetworkSonicL2 Network = "sonic-l2"
)

// ValidNetwork checks whether n is a recognised network constant.
func ValidNetwork(n string) bool {
	switch Network(n) {
	case NetworkSolanaMainnet, NetworkSonicL2:
		return true
	default:
		return false
	}
}

// ChainTx represents a single on-chain transaction tracked by the platform.
// Maps to the chain_txs table in the database.
type ChainTx struct {
	ID            int64           `json:"id" db:"id"`
	Network       string          `json:"network" db:"network"`
	TxHash        string          `json:"tx_hash" db:"tx_hash"`
	TxType        string          `json:"tx_type" db:"tx_type"` // deposit, mint, burn, payout
	Status        string          `json:"status" db:"status"`   // submitted, confirmed, finalized, failed
	Confirmations int             `json:"confirmations" db:"confirmations"`
	RawJSON       json.RawMessage `json:"raw_json" db:"raw_json"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	FinalizedAt   *time.Time      `json:"finalized_at,omitempty" db:"finalized_at"`
}

// Chain transaction types.
const (
	TxTypeDeposit  = "deposit"
	TxTypeMint     = "mint"
	TxTypeBurn     = "burn"
	TxTypePayout   = "payout"
	TxTypeTransfer = "transfer"
)

// Chain transaction statuses.
const (
	TxStatusSubmitted = "submitted"
	TxStatusConfirmed = "confirmed"
	TxStatusFinalized = "finalized"
	TxStatusFailed    = "failed"
)

// DepositEvent represents a deposit parsed from on-chain data.
// This is the canonical event that the chain adapter emits to downstream
// services (mint-service) when a user deposit is detected.
type DepositEvent struct {
	Network         string    `json:"network"`
	TxHash          string    `json:"tx_hash"`
	FromAddress     string    `json:"from_address"`
	ToAddress       string    `json:"to_address"` // reserve_address
	AmountMinor     int64     `json:"amount_minor"`
	AssetSymbol     string    `json:"asset_symbol"` // e.g. "USDC"
	MintAddress     string    `json:"mint_address"`
	DepositIntentID string    `json:"deposit_intent_id,omitempty"`
	BlockNumber     int64     `json:"block_number"`
	Confirmations   int       `json:"confirmations"`
	Timestamp       time.Time `json:"timestamp"`
}

// ReserveAccount represents a platform-controlled reserve wallet for a
// specific network and asset. Maps to the reserve_accounts table.
type ReserveAccount struct {
	ID                    int64      `json:"id" db:"id"`
	Network               string     `json:"network" db:"network"`
	AssetSymbol           string     `json:"asset_symbol" db:"asset_symbol"`
	Address               string     `json:"address" db:"address"`
	ConfirmedBalanceMinor int64      `json:"confirmed_balance_minor" db:"confirmed_balance_minor"`
	PendingBalanceMinor   int64      `json:"pending_balance_minor" db:"pending_balance_minor"`
	LastReconciledAt      *time.Time `json:"last_reconciled_at,omitempty" db:"last_reconciled_at"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`
}

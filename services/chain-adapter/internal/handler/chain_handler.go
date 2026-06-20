// Package handler 实现 chain-adapter 服务的 HTTP 接口层：
// 链上交易查询、储备账户查询，以及 Phase 3 开发用的充值模拟端点。
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

// ChainHandler exposes HTTP endpoints for chain transaction queries,
// reserve account lookups, and development simulation.
type ChainHandler struct {
	chainRepo         *repository.ChainRepository
	simulateDepositFn SimulateDepositFunc
}

// SimulateDepositFunc is the type for injecting a deposit simulator.
// In production this is nil; in development it bridges to the watcher pipeline.
type SimulateDepositFunc func(event *model.DepositEvent) error

// NewChainHandler creates a new ChainHandler.
func NewChainHandler(repo *repository.ChainRepository, simulateFn SimulateDepositFunc) *ChainHandler {
	return &ChainHandler{
		chainRepo:         repo,
		simulateDepositFn: simulateFn,
	}
}

// GetChainTx handles GET /api/v1/chain/tx/:tx_hash
//
// Returns the on-chain transaction status and metadata.
// Note: network must be provided as a query parameter because tx_hash alone
// is not unique across networks.
//
//	GET /api/v1/chain/tx/5vW...abc?network=solana-mainnet
//
// Response:
//
//	{
//	  "network": "solana-mainnet",
//	  "tx_hash": "5vW...abc",
//	  "tx_type": "deposit",
//	  "status": "confirmed",
//	  "confirmations": 1,
//	  "created_at": "2026-06-04T00:00:00Z"
//	}
func (h *ChainHandler) GetChainTx(c *gin.Context) {
	txHash := c.Param("tx_hash")
	if txHash == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "tx_hash path parameter is required",
			},
		})
		return
	}

	network := c.Query("network")
	if network == "" {
		network = string(model.NetworkSolanaMainnet)
	}
	if !model.ValidNetwork(network) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_NETWORK",
				"message": "network must be 'solana-mainnet' or 'sonic-l2'",
			},
		})
		return
	}

	tx, err := h.chainRepo.GetByTxHash(c.Request.Context(), network, txHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}
	if tx == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "TX_NOT_FOUND",
				"message": "transaction not found for the given network and tx_hash",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"network":       tx.Network,
		"tx_hash":       tx.TxHash,
		"tx_type":       tx.TxType,
		"status":        tx.Status,
		"confirmations": tx.Confirmations,
		"raw_json":      tx.RawJSON,
		"created_at":    tx.CreatedAt.UTC().Format(time.RFC3339),
		"finalized_at": func() *string {
			if tx.FinalizedAt != nil {
				s := tx.FinalizedAt.UTC().Format(time.RFC3339)
				return &s
			}
			return nil
		}(),
	})
}

// GetReserveAccount handles GET /api/v1/chain/reserve/:asset_symbol
//
// Returns the reserve account address and balances for a given asset.
// Network is a required query parameter.
//
//	GET /api/v1/chain/reserve/vUSDC?network=solana-mainnet
//
// Response:
//
//	{
//	  "network": "solana-mainnet",
//	  "asset_symbol": "vUSDC",
//	  "address": "RESERVE_WALLET_SOL_PLACEHOLDER",
//	  "confirmed_balance_minor": 0,
//	  "pending_balance_minor": 0
//	}
func (h *ChainHandler) GetReserveAccount(c *gin.Context) {
	assetSymbol := c.Param("asset_symbol")
	if assetSymbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "asset_symbol path parameter is required",
			},
		})
		return
	}

	network := c.Query("network")
	if network == "" {
		network = string(model.NetworkSolanaMainnet)
	}
	if !model.ValidNetwork(network) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_NETWORK",
				"message": "network must be 'solana-mainnet' or 'sonic-l2'",
			},
		})
		return
	}

	acct, err := h.chainRepo.GetReserveAccount(c.Request.Context(), network, assetSymbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}
	if acct == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "RESERVE_NOT_FOUND",
				"message": "reserve account not found for the given network and asset_symbol",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"network":                 acct.Network,
		"asset_symbol":            acct.AssetSymbol,
		"address":                 acct.Address,
		"confirmed_balance_minor": acct.ConfirmedBalanceMinor,
		"pending_balance_minor":   acct.PendingBalanceMinor,
		"last_reconciled_at":      acct.LastReconciledAt,
	})
}

// ListReserveAccounts handles GET /api/v1/chain/reserve
//
// Returns all reserve accounts, optionally filtered by network.
//
//	GET /api/v1/chain/reserve
//	GET /api/v1/chain/reserve?network=solana-mainnet
func (h *ChainHandler) ListReserveAccounts(c *gin.Context) {
	network := c.Query("network")

	accounts, err := h.chainRepo.ListReserveAccounts(c.Request.Context(), network)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "QUERY_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	type reserveAccountResponse struct {
		Network               string  `json:"network"`
		AssetSymbol           string  `json:"asset_symbol"`
		Address               string  `json:"address"`
		ConfirmedBalanceMinor int64   `json:"confirmed_balance_minor"`
		PendingBalanceMinor   int64   `json:"pending_balance_minor"`
		LastReconciledAt      *string `json:"last_reconciled_at,omitempty"`
	}

	results := make([]reserveAccountResponse, 0, len(accounts))
	for _, a := range accounts {
		item := reserveAccountResponse{
			Network:               a.Network,
			AssetSymbol:           a.AssetSymbol,
			Address:               a.Address,
			ConfirmedBalanceMinor: a.ConfirmedBalanceMinor,
			PendingBalanceMinor:   a.PendingBalanceMinor,
		}
		if a.LastReconciledAt != nil {
			s := a.LastReconciledAt.UTC().Format(time.RFC3339)
			item.LastReconciledAt = &s
		}
		results = append(results, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"accounts": results,
	})
}

// SimulateDeposit handles POST /api/v1/chain/simulate-deposit
//
// Phase 3 development endpoint: injects a synthetic deposit event into the
// watcher pipeline. This allows testing the full deposit->mint flow without
// a live blockchain connection.
//
// Request:
//
//	{
//	  "network": "solana-mainnet",
//	  "tx_hash": "sim_01J...abc",
//	  "from_address": "USER_WALLET",
//	  "to_address": "RESERVE_WALLET_SOL_PLACEHOLDER",
//	  "amount_minor": 50000000,
//	  "asset_symbol": "vUSDC",
//	  "mint_address": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
//	  "block_number": 999999
//	}
func (h *ChainHandler) SimulateDeposit(c *gin.Context) {
	// This endpoint is a development-only tool; reject in production.
	// Gate it behind an env-var or build tag in a real deployment.

	if h.simulateDepositFn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"code":    "NOT_AVAILABLE",
				"message": "deposit simulation is not available (no simulator configured)",
			},
		})
		return
	}

	var req struct {
		Network         string `json:"network"`
		TxHash          string `json:"tx_hash"`
		FromAddress     string `json:"from_address"`
		ToAddress       string `json:"to_address"`
		AmountMinor     int64  `json:"amount_minor"`
		AssetSymbol     string `json:"asset_symbol"`
		MintAddress     string `json:"mint_address"`
		DepositIntentID string `json:"deposit_intent_id"`
		BlockNumber     int64  `json:"block_number"`
		Confirmations   int    `json:"confirmations"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
		return
	}

	if req.Network == "" || req.TxHash == "" || req.FromAddress == "" ||
		req.ToAddress == "" || req.AssetSymbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "network, tx_hash, from_address, to_address, and asset_symbol are required",
			},
		})
		return
	}
	if req.AmountMinor <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PARAMETER",
				"message": "amount_minor must be positive",
			},
		})
		return
	}
	if !model.ValidNetwork(req.Network) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_NETWORK",
				"message": "network must be 'solana-mainnet' or 'sonic-l2'",
			},
		})
		return
	}

	event := &model.DepositEvent{
		Network:         req.Network,
		TxHash:          req.TxHash,
		FromAddress:     req.FromAddress,
		ToAddress:       req.ToAddress,
		AmountMinor:     req.AmountMinor,
		AssetSymbol:     req.AssetSymbol,
		MintAddress:     req.MintAddress,
		DepositIntentID: req.DepositIntentID,
		BlockNumber:     req.BlockNumber,
		Confirmations:   req.Confirmations,
		Timestamp:       time.Now().UTC(),
	}

	if err := h.simulateDepositFn(event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"code":    "SIMULATE_FAILED",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":            "deposit_simulated",
		"network":           event.Network,
		"tx_hash":           event.TxHash,
		"amount_minor":      event.AmountMinor,
		"asset_symbol":      event.AssetSymbol,
		"deposit_intent_id": event.DepositIntentID,
		"from_address":      event.FromAddress,
		"timestamp":         event.Timestamp.UTC().Format(time.RFC3339),
	})
}

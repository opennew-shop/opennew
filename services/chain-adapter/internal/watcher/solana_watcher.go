package watcher

import (
	"context"
	"time"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/model"
	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/repository"
)

// SolanaDepositWatcher implements DepositWatcher with Solana-specific deposit
// polling logic. It queries the Solana RPC endpoint for transactions involving
// the configured reserve addresses and parses SPL Token transfers (USDC).
//
// Phase 3: The RPC integration is a skeleton. Specific calls (getSignaturesForAddress,
// getTransaction, getParsedTransaction) are documented inline. The watcher is
// wired up and can be driven by the simulate-deposit endpoint for development.
type SolanaDepositWatcher struct {
	*DepositWatcher
}

// NewSolanaDepositWatcher creates a new SolanaDepositWatcher with 10-second polling.
func NewSolanaDepositWatcher(
	rpcEndpoint string,
	reserveAddresses map[string]string,
	repo *repository.ChainRepository,
) *SolanaDepositWatcher {
	base := NewDepositWatcher(
		model.NetworkSolanaMainnet,
		rpcEndpoint,
		reserveAddresses,
		repo,
		nil, // eventHandler wired after construction
	)
	base.PollInterval = 10 * time.Second
	return &SolanaDepositWatcher{DepositWatcher: base}
}

// SetEventHandler sets the deposit event handler callback. Call after
// construction, before calling Start.
func (w *SolanaDepositWatcher) SetEventHandler(handler DepositEventHandler) {
	w.EventHandler = handler
}

// ProcessDepositEvent exposes the base processEvent method publicly. It is
// used by the simulate-deposit endpoint (Phase 3 development tool) to inject
// synthetic deposit events into the full processing pipeline.
func (w *SolanaDepositWatcher) ProcessDepositEvent(ctx context.Context, event *model.DepositEvent) error {
	return w.processEvent(ctx, event)
}

// pollDeposits implements Solana-specific deposit polling.
//
// Typical Solana RPC flow (documented, skeleton in Phase 3):
//
//  1. For each reserve address, call getSignaturesForAddress with
//     limit=20 and before=lastBlock to fetch recent transaction signatures.
//
//  2. For each signature, call getTransaction with
//     encoding=jsonParsed and maxSupportedTransactionVersion=0
//     to retrieve the full parsed transaction.
//
//  3. Inspect the parsed transaction's inner instructions and token balances
//     to detect SPL Token transfers where:
//       - destination == reserve address
//       - mint == USDC mint (EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v)
//       - amount > 0
//
//  4. Extract the source wallet from the pre-token-balances.
//
//  5. Build a DepositEvent and call processEvent.
//
//  6. Update lastBlock to the highest processed slot.
//
// Phase 3 fallback: this implementation logs its intent and returns.
// The simulate-deposit endpoint drives the event pipeline for testing.
func (w *SolanaDepositWatcher) pollDeposits(ctx context.Context) {
	w.logger.Info("solana pollDeposits — skeleton (use POST /api/v1/chain/simulate-deposit for Phase 3 testing)")

	// Build a reverse lookup: address -> assetSymbol.
	addrToSymbol := make(map[string]string, len(w.ReserveAddresses))
	for symbol, addr := range w.ReserveAddresses {
		addrToSymbol[addr] = symbol
	}

	// ---------------------------------------------------------------
	// Phase 3 skeleton — uncomment and implement when RPC is available:
	// ---------------------------------------------------------------
	//
	// client := rpc.New(w.RpcEndpoint)
	//
	// fromSlot := w.LastBlock()
	// if fromSlot == 0 {
	//     // On first run, start from recent confirmed slot.
	//     slot, err := client.GetSlot(ctx, rpc.CommitmentConfirmed)
	//     if err != nil { return }
	//     fromSlot = int64(slot) - 100
	// }
	//
	// for assetSymbol, reserveAddr := range w.ReserveAddresses {
	//     sigs, err := client.GetSignaturesForAddress(ctx, reserveAddr, &rpc.GetSignaturesForAddressOpts{
	//         Limit:  20,
	//         Before: "",  // Solana uses signature-based pagination, not slot.
	//     })
	//     if err != nil {
	//         w.logger.Warn("getSignaturesForAddress failed", "address", reserveAddr, "error", err)
	//         continue
	//     }
	//
	//     for _, sig := range sigs {
	//         if sig.Slot <= uint64(fromSlot) {
	//             continue
	//         }
	//
	//         // Check if already processed.
	//         existing, _ := w.ChainRepo.GetByTxHash(ctx, string(w.Network), sig.Signature)
	//         if existing != nil {
	//             continue
	//         }
	//
	//         tx, err := client.GetTransaction(ctx, sig.Signature, &rpc.GetTransactionOpts{
	//             Encoding:                       rpc.EncodingJSONParsed,
	//             MaxSupportedTransactionVersion: pointer.To(uint8(0)),
	//         })
	//         if err != nil {
	//             w.logger.Warn("getTransaction failed", "sig", sig.Signature, "error", err)
	//             continue
	//         }
	//
	//         event := parseSolanaUSDCTransfer(tx, reserveAddr, assetSymbol)
	//         if event != nil {
	//             w.processEvent(ctx, event)
	//         }
	//
	//         if int64(sig.Slot) > w.lastBlock {
	//             w.mu.Lock()
	//             w.lastBlock = int64(sig.Slot)
	//             w.mu.Unlock()
	//         }
	//     }
	// }
	//
	// w.logger.Debug("solana poll complete", "last_block", w.LastBlock())
}

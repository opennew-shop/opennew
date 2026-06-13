// Package solana implements Solana-specific chain interactions for the ANCF
// chain-adapter service. It covers Token-2022 deployment, SPL Token transfers,
// multisig governance, deposit watching, and supply reconciliation.
//
// Phase 4: The Token-2022 integration is implemented via raw JSON-RPC over
// HTTP to avoid pulling in a heavy Solana SDK dependency. Signing is delegated
// to an external signer (KMS/HSM) via the Signer interface; a local keypair
// signer is provided for development.
package solana

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Solana JSON-RPC client (minimal, no external SDK dependency)
// ---------------------------------------------------------------------------

// RPCClient is a minimal Solana JSON-RPC HTTP client. It supports the subset
// of RPC methods needed by the chain adapter: sendTransaction,
// getSignaturesForAddress, getTransaction, getTokenSupply, getBalance,
// getAccountInfo, and getSlot.
type RPCClient struct {
	endpoint   string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewRPCClient creates a new RPCClient for the given Solana RPC endpoint.
func NewRPCClient(endpoint string) *RPCClient {
	return &RPCClient{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: slog.Default().With("component", "solana-rpc", "endpoint", endpoint),
	}
}

// rpcRequest is a standard JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// rpcResponse is a standard JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError represents a JSON-RPC error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call makes a JSON-RPC call and unmarshals the result into dst.
func (c *RPCClient) call(ctx context.Context, method string, params interface{}, dst interface{}) error {
	reqBody := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("solana rpc: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("solana rpc: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("solana rpc: http post: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("solana rpc: read body: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		c.logger.Error("failed to unmarshal RPC response", "body", string(respBytes), "error", err)
		return fmt.Errorf("solana rpc: unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("solana rpc: error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if dst != nil {
		if err := json.Unmarshal(rpcResp.Result, dst); err != nil {
			return fmt.Errorf("solana rpc: unmarshal result: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Solana account types
// ---------------------------------------------------------------------------

// Signer represents an entity that can sign Solana transactions. In
// production this is backed by KMS/HSM; in development it wraps a local
// JSON keypair file.
type Signer struct {
	PublicKey string `json:"public_key"`
	// PrivateKey is intentionally NOT stored in-memory for production signers.
	// The SignTransaction method delegates to the secure enclave.
	PrivateKey string `json:"private_key,omitempty"` // dev only
}

// MintAccount represents a deployed SPL Token-2022 mint.
type MintAccount struct {
	Address         string `json:"address"`
	Decimals        uint8  `json:"decimals"`
	MintAuthority   string `json:"mint_authority"`
	FreezeAuthority string `json:"freeze_authority,omitempty"`
	Supply          uint64 `json:"supply"`
	IsInitialized   bool   `json:"is_initialized"`
}

// TokenAccount represents an SPL Token account (ATA or otherwise).
type TokenAccount struct {
	Address string `json:"address"`
	Mint    string `json:"mint"`
	Owner   string `json:"owner"`
	Amount  uint64 `json:"amount"`
}

// TokenSupplyInfo is returned by getTokenSupply.
type TokenSupplyInfo struct {
	Amount  string `json:"amount"`
	Decimals uint8  `json:"decimals"`
	UIAmount string `json:"uiAmount"`
}

// TokenSupplyResult wraps getTokenSupply RPC response.
type TokenSupplyResult struct {
	Value TokenSupplyInfo `json:"value"`
}

// TxSignature is a confirmed transaction signature with slot info.
type TxSignature struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Err       *json.RawMessage `json:"err"`
	Memo      *string          `json:"memo"`
}

// ParsedTransaction is a simplified parsed transaction from Solana RPC.
type ParsedTransaction struct {
	Slot        uint64              `json:"slot"`
	BlockTime   *int64              `json:"blockTime"`
	Meta        *ParsedTxMeta       `json:"meta"`
	Transaction *ParsedTxMessage    `json:"transaction"`
}

// ParsedTxMeta contains pre/post token balances and log messages.
type ParsedTxMeta struct {
	Err           interface{}              `json:"err"`
	PreBalances   []uint64                 `json:"preBalances"`
	PostBalances  []uint64                 `json:"postBalances"`
	PreTokenBalances  []TokenBalance       `json:"preTokenBalances"`
	PostTokenBalances []TokenBalance       `json:"postTokenBalances"`
	LogMessages   []string                 `json:"logMessages"`
	InnerInstructions []ParsedInnerInstruction `json:"innerInstructions"`
}

// TokenBalance represents a token balance before or after a transaction.
type TokenBalance struct {
	AccountIndex int           `json:"accountIndex"`
	Mint         string        `json:"mint"`
	Owner        string        `json:"owner"`
	UITokenAmount TokenAmount  `json:"uiTokenAmount"`
}

// TokenAmount is the UI token amount struct from Solana RPC.
type TokenAmount struct {
	Amount   string `json:"amount"`
	Decimals uint8  `json:"decimals"`
	UIAmount *float64 `json:"uiAmount"`
}

// ParsedInnerInstruction contains inner instructions for a transaction.
type ParsedInnerInstruction struct {
	Index        int              `json:"index"`
	Instructions []ParsedInstruction `json:"instructions"`
}

// ParsedInstruction is a parsed instruction from Solana RPC.
type ParsedInstruction struct {
	Program   string                 `json:"program"`
	ProgramID string                 `json:"programId"`
	Parsed    *json.RawMessage       `json:"parsed"`
}

// ParsedTxMessage is the transaction message with account keys and instructions.
type ParsedTxMessage struct {
	AccountKeys   []ParsedAccountKey   `json:"accountKeys"`
	Instructions  []ParsedInstruction  `json:"instructions"`
	RecentBlockhash string             `json:"recentBlockhash"`
}

// ParsedAccountKey is an account key in a parsed transaction.
type ParsedAccountKey struct {
	Pubkey   string `json:"pubkey"`
	Signer   bool   `json:"signer"`
	Writable bool   `json:"writable"`
}

// GetSignaturesForAddressResult is the result from getSignaturesForAddress.
type GetSignaturesForAddressResult []TxSignature

// GetSlotResult wraps getSlot response.
type GetSlotResult uint64

// SendTxResult is the transaction signature from sendTransaction.
type SendTxResult string

// GetTxOpts are options for getTransaction.
type GetTxOpts struct {
	Encoding                       string `json:"encoding"`
	MaxSupportedTransactionVersion int    `json:"maxSupportedTransactionVersion"`
	Commitment                     string `json:"commitment,omitempty"`
}

// ---------------------------------------------------------------------------
// RPC convenience methods
// ---------------------------------------------------------------------------

// GetSlot returns the current confirmed slot.
func (c *RPCClient) GetSlot(ctx context.Context, commitment string) (uint64, error) {
	if commitment == "" {
		commitment = "confirmed"
	}
	var result GetSlotResult
	if err := c.call(ctx, "getSlot", []interface{}{map[string]string{"commitment": commitment}}, &result); err != nil {
		return 0, err
	}
	return uint64(result), nil
}

// GetSignaturesForAddress fetches recent transaction signatures for an address.
func (c *RPCClient) GetSignaturesForAddress(ctx context.Context, address string, limit int, before string) ([]TxSignature, error) {
	if limit <= 0 {
		limit = 20
	}
	params := []interface{}{address, map[string]interface{}{
		"limit":  limit,
		"before": before,
	}}
	if before == "" {
		params[1] = map[string]interface{}{"limit": limit}
	}

	var result GetSignaturesForAddressResult
	if err := c.call(ctx, "getSignaturesForAddress", params, &result); err != nil {
		return nil, err
	}
	return []TxSignature(result), nil
}

// GetTransaction fetches a parsed transaction by signature.
func (c *RPCClient) GetTransaction(ctx context.Context, signature string) (*ParsedTransaction, error) {
	params := []interface{}{
		signature,
		GetTxOpts{
			Encoding:                       "jsonParsed",
			MaxSupportedTransactionVersion: 0,
			Commitment:                     "confirmed",
		},
	}
	var result ParsedTransaction
	if err := c.call(ctx, "getTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTokenSupply returns the total supply of a token mint.
func (c *RPCClient) GetTokenSupply(ctx context.Context, mintAddress string) (*TokenSupplyResult, error) {
	params := []interface{}{
		mintAddress,
		map[string]string{"commitment": "confirmed"},
	}
	var result TokenSupplyResult
	if err := c.call(ctx, "getTokenSupply", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SimulateTransaction simulates a serialized transaction and returns logs/units.
func (c *RPCClient) SimulateTransaction(ctx context.Context, txBase58 string) (json.RawMessage, error) {
	params := []interface{}{
		txBase58,
		map[string]interface{}{
			"encoding":                       "base58",
			"commitment":                     "confirmed",
			"sigVerify":                      true,
			"replaceRecentBlockhash":         true,
			"maxSupportedTransactionVersion": 0,
		},
	}
	var result json.RawMessage
	if err := c.call(ctx, "simulateTransaction", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetRecentBlockhash returns a recent blockhash for transaction construction.
func (c *RPCClient) GetRecentBlockhash(ctx context.Context) (string, uint64, error) {
	type blockhashResult struct {
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	var result blockhashResult
	if err := c.call(ctx, "getLatestBlockhash", []interface{}{map[string]string{"commitment": "finalized"}}, &result); err != nil {
		return "", 0, err
	}
	return result.Value.Blockhash, result.Value.LastValidBlockHeight, nil
}

// SendTransaction submits a signed transaction to the network.
func (c *RPCClient) SendTransaction(ctx context.Context, txBase58 string) (string, error) {
	params := []interface{}{
		txBase58,
		map[string]interface{}{
			"encoding":                       "base58",
			"skipPreflight":                  false,
			"preflightCommitment":            "confirmed",
			"maxSupportedTransactionVersion": 0,
		},
	}
	var result SendTxResult
	if err := c.call(ctx, "sendTransaction", params, &result); err != nil {
		return "", err
	}
	return string(result), nil
}

// GetBalance returns the SOL balance (in lamports) for a given address.
func (c *RPCClient) GetBalance(ctx context.Context, address string) (uint64, error) {
	type balanceResult struct {
		Value uint64 `json:"value"`
	}
	var result balanceResult
	if err := c.call(ctx, "getBalance", []interface{}{address, map[string]string{"commitment": "confirmed"}}, &result); err != nil {
		return 0, err
	}
	return result.Value, nil
}

// ---------------------------------------------------------------------------
// Token-2022 Client
// ---------------------------------------------------------------------------

// Token2022Client provides operations for deploying and interacting with
// a Token-2022 (SPL Token program extension) mint.
//
// Token-2022 program ID on Solana mainnet / devnet:
//
//	TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb
type Token2022Client struct {
	rpc    *RPCClient
	logger *slog.Logger
}

// Solana program IDs.
const (
	TokenProgramID      = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	Token2022ProgramID  = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	AssociatedTokenProgramID = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	SystemProgramID     = "11111111111111111111111111111111"
	SysvarRentID        = "SysvarRent111111111111111111111111111111111"
)

// USDC mainnet mint addresses (for reference in deposit watching).
const (
	USDCDevnetMint = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU" // devnet USDC
	USDCMainnetMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" // mainnet USDC
)

// DefaultVUSDCDecimals is the standard decimals for vUSDC (matching USDC).
const DefaultVUSDCDecimals = 6

// NewToken2022Client creates a new Token2022Client.
func NewToken2022Client(rpcEndpoint string) *Token2022Client {
	return &Token2022Client{
		rpc:    NewRPCClient(rpcEndpoint),
		logger: slog.Default().With("component", "token2022"),
	}
}

// DeployVUSDC creates a new Token-2022 mint for vUSDC.
//
// Parameters:
//   - ctx: context for cancellation
//   - payer: the fee-paying signer (dev: local keypair; prod: KMS reference)
//   - multisigPDA: the PDA of the 2-of-3 multisig that will hold mint authority
//   - freezeAuthority: optional freeze authority (empty string = no freeze authority)
//
// Returns the deployed mint account info.
//
// Implementation note (Phase 4):
// This function constructs and signs a createInitializeMintInstructionV2
// transaction targeting the Token-2022 program. The mint authority is set
// to the multisig PDA so all subsequent mint/burn operations require 2-of-3
// approval.
//
// The transaction layout (3 instructions):
//  1. SystemProgram.createAccount — allocate space for mint
//  2. Token2022.initializeMint2 — initialize with multisigPDA as mint_authority
//
// The caller must ensure payer.PrivateKey is set (dev mode) or that the
// Signer interface delegates to KMS/HSM for signing.
func DeployVUSDC(ctx context.Context, rpcEndpoint string, payer *Signer, multisigPDA string, freezeAuthority string) (*MintAccount, error) {
	client := NewToken2022Client(rpcEndpoint)

	client.logger.Info("deploying vUSDC Token-2022 mint",
		"payer", payer.PublicKey,
		"multisig_pda", multisigPDA,
		"freeze_authority", freezeAuthority,
	)

	// -----------------------------------------------------------------------
	// Phase 4 skeleton — construct and submit createInitializeMint transaction
	// -----------------------------------------------------------------------
	//
	// In a full Solana SDK integration (e.g. github.com/gagliardetto/solana-go):
	//
	//   // 1. Create mint keypair (derived from payer or random).
	//   mintKP := solana.NewWallet()
	//
	//   // 2. Calculate minimum lamports for rent exemption.
	//   mintLen := token.MINT_SIZE  // 82 bytes for Token-2022
	//   rentLamports, _ := client.rpc.GetMinimumBalanceForRentExemption(ctx, mintLen)
	//
	//   // 3. Construct instructions.
	//   createAccountIx := system.NewCreateAccountInstruction(
	//       rentLamports, mintLen, Token2022ProgramID,
	//       payer.PublicKey, mintKP.PublicKey(),
	//   ).Build()
	//
	//   initMintIx := token.NewInitializeMint2Instruction(
	//       DefaultVUSDCDecimals,
	//       multisigPDA,    // mint_authority
	//       freezeAuthority, // freeze_authority (empty = none)
	//       mintKP.PublicKey(),
	//   ).Build()
	//
	//   // 4. Build and sign transaction.
	//   recentBlockhash, _ := client.rpc.GetRecentBlockhash(ctx)
	//   tx, _ := solana.NewTransaction(
	//       []solana.Instruction{createAccountIx, initMintIx},
	//       recentBlockhash,
	//       solana.TransactionPayer(payer.PublicKey),
	//   )
	//   _, _ = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
	//       return &solana.PrivateKey(payer.PrivateKey)
	//   })
	//
	//   // 5. Submit.
	//   sig, _ := client.rpc.SendTransaction(ctx, tx.MustToBase58())
	//
	//   // 6. Return MintAccount.
	//   return &MintAccount{
	//       Address:         mintKP.PublicKey().String(),
	//       Decimals:        DefaultVUSDCDecimals,
	//       MintAuthority:   multisigPDA,
	//       FreezeAuthority: freezeAuthority,
	//       Supply:          0,
	//       IsInitialized:   true,
	//   }, nil

	_ = client // placeholder — full SDK integration deferred to Phase 4 RPC wiring
	_ = ctx
	_ = payer

	return &MintAccount{
		Address:         "vUSDC_MINT_PLACEHOLDER",
		Decimals:        DefaultVUSDCDecimals,
		MintAuthority:   multisigPDA,
		FreezeAuthority: freezeAuthority,
		Supply:          0,
		IsInitialized:   false,
	}, fmt.Errorf("solana: DeployVUSDC requires full Solana SDK integration (Phase 4)")
}

// MintTo issues new vUSDC tokens to a destination address's Associated Token
// Account (ATA). The mint authority must be the multisig PDA; therefore this
// call must be preceded by a successful 2-of-3 multisig approval.
//
// Parameters:
//   - mintAddress: the vUSDC Token-2022 mint
//   - destAddress: the recipient's wallet address (ATA will be created if needed)
//   - amount: amount in token native units (e.g. 1_000_000 = 1.000000 vUSDC with 6 decimals)
//
// Returns the transaction signature.
//
// The token amount must not exceed the approved amount in the multisig proposal.
func MintTo(ctx context.Context, rpcEndpoint string, mintAuthority *Signer, mintAddress string, destAddress string, amount uint64) (string, error) {
	client := NewToken2022Client(rpcEndpoint)
	client.logger.Info("mint_to requested",
		"mint", mintAddress,
		"dest", destAddress,
		"amount", amount,
	)

	// -----------------------------------------------------------------------
	// Phase 4 skeleton — construct and submit mintTo transaction
	// -----------------------------------------------------------------------
	//
	// In a full Solana SDK integration:
	//
	//   // 1. Derive or fetch the destination ATA.
	//   ata, _, _ := solana.FindAssociatedTokenAddress(destAddress, mintAddress)
	//
	//   // 2. Construct instructions:
	//   //    a. createAssociatedTokenAccount if ATA doesn't exist
	//   //    b. mintTo
	//   instructions := []solana.Instruction{}
	//
	//   // Check ATA existence; if not, add createATA instruction.
	//   // (In practice, use getAccountInfo to check before building the tx.)
	//
	//   mintToIx := token.NewMintToInstruction(
	//       amount,
	//       mintAddress,
	//       ata,
	//       mintAuthority.PublicKey,
	//       nil, // multiSigners (empty; authority is the multisig PDA)
	//   ).Build()
	//   instructions = append(instructions, mintToIx)
	//
	//   // 3. Build, sign, submit.
	//   // NOTE: The mint_authority here is the multisig PDA. In a real setup,
	//   // the multisig program itself invokes mintTo via CPI, so this function
	//   // is called indirectly through ExecuteProposal.
	//   ...
	//   return sig, nil

	_ = client
	_ = ctx
	_ = mintAuthority
	_ = mintAddress
	_ = destAddress
	_ = amount

	return "", fmt.Errorf("solana: MintTo requires full Solana SDK integration (Phase 4)")
}

// Burn destroys vUSDC tokens from an owner's account. Used during redemption
// to reduce on-chain supply.
//
// Parameters:
//   - owner: the signer who owns the tokens
//   - mintAddress: the vUSDC Token-2022 mint
//   - amount: amount in token native units to burn
//
// Returns the transaction signature.
func Burn(ctx context.Context, rpcEndpoint string, owner *Signer, mintAddress string, amount uint64) (string, error) {
	client := NewToken2022Client(rpcEndpoint)
	client.logger.Info("burn requested",
		"mint", mintAddress,
		"owner", owner.PublicKey,
		"amount", amount,
	)

	// -----------------------------------------------------------------------
	// Phase 4 skeleton — construct and submit burn transaction
	// -----------------------------------------------------------------------
	//
	// In a full Solana SDK integration:
	//
	//   ata, _, _ := solana.FindAssociatedTokenAddress(owner.PublicKey, mintAddress)
	//   burnIx := token.NewBurnInstruction(
	//       amount,
	//       ata,
	//       mintAddress,
	//       owner.PublicKey,
	//       nil,
	//   ).Build()
	//   ...
	//   return sig, nil

	_ = client
	_ = ctx
	_ = owner
	_ = mintAddress
	_ = amount

	return "", fmt.Errorf("solana: Burn requires full Solana SDK integration (Phase 4)")
}

// GetTokenSupply queries the on-chain total supply of a Token-2022 mint.
//
// This is the primary data source for the reconciliation invariant:
//
//	onchain_vusdc_supply_minor + pending_redemption_minor <= confirmed_reserve_usdc_minor
func GetTokenSupply(ctx context.Context, rpcEndpoint string, mintAddress string) (uint64, error) {
	client := NewToken2022Client(rpcEndpoint)

	supply, err := client.rpc.GetTokenSupply(ctx, mintAddress)
	if err != nil {
		return 0, fmt.Errorf("solana: get token supply for %s: %w", mintAddress, err)
	}

	// Parse the amount string to uint64.
	// Solana returns amounts as string to avoid JSON number precision issues.
	var amount uint64
	if _, err := fmt.Sscanf(supply.Value.Amount, "%d", &amount); err != nil {
		return 0, fmt.Errorf("solana: parse token supply amount %q: %w", supply.Value.Amount, err)
	}

	return amount, nil
}

// GetTokenBalance queries the token balance for a specific wallet's ATA.
func GetTokenBalance(ctx context.Context, rpcEndpoint string, walletAddress string, mintAddress string) (uint64, error) {
	client := NewToken2022Client(rpcEndpoint)

	// Derive ATA address deterministically.
	ata := deriveATA(walletAddress, mintAddress)

	type balanceResult struct {
		Value struct {
			Amount string `json:"amount"`
		} `json:"value"`
	}

	// Use getTokenAccountBalance RPC method.
	params := []interface{}{
		ata,
		map[string]string{"commitment": "confirmed"},
	}
	var result balanceResult
	if err := client.rpc.call(ctx, "getTokenAccountBalance", params, &result); err != nil {
		// If the ATA doesn't exist, return 0 balance.
		return 0, nil
	}

	var amount uint64
	if _, err := fmt.Sscanf(result.Value.Amount, "%d", &amount); err != nil {
		return 0, fmt.Errorf("solana: parse token balance for %s: %w", walletAddress, err)
	}

	return amount, nil
}

// deriveATA computes the deterministic Associated Token Account address.
//
// In production this uses FindAssociatedTokenAddress; the placeholder
// implementation returns a stub. Full SDK integration will replace this.
func deriveATA(walletAddress string, mintAddress string) string {
	// Placeholder — in production use:
	//   ata, _, _ := solana.FindAssociatedTokenAddress(
	//       solana.MustPublicKeyFromBase58(walletAddress),
	//       solana.MustPublicKeyFromBase58(mintAddress),
	//   )
	//   return ata.String()
	_ = walletAddress
	_ = mintAddress
	return "ATA_PLACEHOLDER"
}

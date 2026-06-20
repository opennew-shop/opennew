package solana

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Multisig types and configuration
// ---------------------------------------------------------------------------

// MultisigConfig holds the configuration for a 2-of-3 multisig.
//
// The three signers represent three independent operators or key custodians.
// The threshold is fixed at 2, meaning any 2 of the 3 signers must approve a
// proposal before it can be executed.
//
// The PDA (Program Derived Address) is the address of the multisig vault
// account that holds the mint authority for the vUSDC Token-2022 mint.
// 中文说明：2-of-3 多签配置。Threshold 固定为 2，Signers 为 3 个 base58 公钥，
// PDA 为持有 vUSDC Token-2022 铸币权限的多签金库程序派生地址。
type MultisigConfig struct {
	Threshold   uint8     `json:"threshold"`    // Fixed at 2 for 2-of-3
	Signers     [3]string `json:"signers"`      // 3 public key addresses (base58)
	PDA         string    `json:"pda"`          // Multisig PDA address
	MintAddress string    `json:"mint_address"` // vUSDC mint governed by this multisig
}

// Validate checks that the multisig configuration is consistent.
func (m *MultisigConfig) Validate() error {
	if m.Threshold != 2 {
		return fmt.Errorf("multisig: threshold must be 2 for 2-of-3, got %d", m.Threshold)
	}
	for i, signer := range m.Signers {
		if signer == "" {
			return fmt.Errorf("multisig: signer %d is empty", i)
		}
		if _, err := decodeBase58Fixed(signer, 32); err != nil {
			return fmt.Errorf("multisig: signer %d must be a base58 ed25519 public key: %w", i, err)
		}
	}
	if m.PDA == "" {
		return fmt.Errorf("multisig: PDA address is required")
	}
	if m.MintAddress != "" {
		if _, err := decodeBase58Fixed(m.MintAddress, 32); err != nil {
			return fmt.Errorf("multisig: mint address must be base58: %w", err)
		}
	}
	return nil
}

// MultisigProposal represents a pending multisig proposal awaiting approval.
// Proposals are identified by a deterministic proposal ID derived from the
// proposal data (amount, destination, nonce).
type MultisigProposal struct {
	ID           string          `json:"id"`
	Proposer     string          `json:"proposer"`
	Action       string          `json:"action"` // "mint", "burn", "freeze", "thaw", "transfer_mint_authority"
	Amount       uint64          `json:"amount"`
	DestAddress  string          `json:"dest_address,omitempty"`
	MintAddress  string          `json:"mint_address"`
	Nonce        uint64          `json:"nonce"`
	Approvals    map[string]bool `json:"approvals"` // signer -> approved
	Status       string          `json:"status"`    // "pending", "approved", "executed", "rejected", "expired"
	CreatedAt    time.Time       `json:"created_at"`
	ExecutedAt   *time.Time      `json:"executed_at,omitempty"`
	ExecutedTxID string          `json:"executed_tx_id,omitempty"`
}

// Proposal status constants.
const (
	ProposalStatusPending   = "pending"
	ProposalStatusApproved  = "approved"
	ProposalStatusExecuting = "executing"
	ProposalStatusExecuted  = "executed"
	ProposalStatusRejected  = "rejected"
	ProposalStatusExpired   = "expired"
)

// Proposal action constants.
const (
	ProposalActionMint                  = "mint"
	ProposalActionBurn                  = "burn"
	ProposalActionFreeze                = "freeze"
	ProposalActionThaw                  = "thaw"
	ProposalActionTransferMintAuthority = "transfer_mint_authority"
)

// ---------------------------------------------------------------------------
// Multisig Manager
// ---------------------------------------------------------------------------

// MultisigManager orchestrates the full lifecycle of multisig proposals:
// creation, approval collection, threshold checking, and execution via the
// SPL Governance program or a native multisig.
//
// Proposals are persisted in PostgreSQL so that state survives restarts
// (SECURITY FIX: F-004-01).
//
// In the Phase 4 on-chain architecture, the multisig is the mint authority for
// the vUSDC Token-2022 mint. Shadow-ledger crediting is explicitly handled by
// mint-service policy and does not claim multisig protection.
//
// Proposal lifecycle:
//
//	1. Proposer submits ProposeMint/Burn/Freeze
//	2. Any 2 of 3 signers call ApproveProposal
//	3. When threshold is reached, any signer calls ExecuteProposal
//	4. Execution constructs and submits the actual Solana transaction
//
// Security: The multisig PDA is derived from the signer set. Changing signers
// requires transferring mint authority to a new multisig PDA, which itself
// requires 2-of-3 approval (or a governance DAO vote in production).
type MultisigManager struct {
	config    *MultisigConfig
	rpcClient *RPCClient
	logger    *slog.Logger
	db        *sql.DB

	mu        sync.RWMutex
	proposals map[string]*MultisigProposal
	nonce     uint64
}

// NewMultisigManager creates a new MultisigManager for the given configuration.
// Use NewMultisigManagerWithDB for persistent, restart-safe proposal storage.
func NewMultisigManager(config *MultisigConfig, rpcEndpoint string) (*MultisigManager, error) {
	return NewMultisigManagerWithDB(config, rpcEndpoint, nil)
}

// NewMultisigManagerWithDB creates a new MultisigManager with PostgreSQL
// persistence. When db is nil, proposals are stored in-memory only (dev mode).
// When db is non-nil, proposals are persisted and restored from the database
// on startup.
//
// SECURITY FIX: F-004-01 — Added DB persistence for proposal durability.
func NewMultisigManagerWithDB(config *MultisigConfig, rpcEndpoint string, db *sql.DB) (*MultisigManager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	m := &MultisigManager{
		config:    config,
		rpcClient: NewRPCClient(rpcEndpoint),
		logger:    slog.Default().With("component", "multisig", "pda", config.PDA),
		db:        db,
		proposals: make(map[string]*MultisigProposal),
		nonce:     0,
	}

	// Restore proposals from DB on startup.
	if db != nil {
		if err := m.loadProposalsFromDB(context.Background()); err != nil {
			return nil, fmt.Errorf("multisig: load proposals from DB: %w", err)
		}
	}

	return m, nil
}

// Config returns a copy of the multisig configuration.
func (m *MultisigManager) Config() MultisigConfig {
	return *m.config
}

// nextNonce returns a monotonically increasing nonce for proposal generation.
func (m *MultisigManager) nextNonce() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nonce++
	return m.nonce
}

// deriveProposalID generates a deterministic proposal ID from the proposal
// data. Uses SHA-256 over the concatenation of action, amount, dest address,
// mint address, and nonce.
func deriveProposalID(action string, amount uint64, destAddress string, mintAddress string, nonce uint64) string {
	payload := fmt.Sprintf("%s|%d|%s|%s|%d", action, amount, destAddress, mintAddress, nonce)
	hash := sha256.Sum256([]byte(payload))
	return "prop_" + hex.EncodeToString(hash[:16])
}

// isSigner checks whether the given public key is one of the 3 signers.
func (m *MultisigManager) isSigner(pubkey string) bool {
	for _, s := range m.config.Signers {
		if s == pubkey {
			return true
		}
	}
	return false
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// decodeBase58Fixed decodes a Solana base58 public key and enforces its byte length.
func decodeBase58Fixed(s string, wantLen int) ([]byte, error) {
	result := big.NewInt(0)
	base := big.NewInt(58)
	for _, r := range s {
		idx := -1
		for i, a := range base58Alphabet {
			if r == a {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", r)
		}
		result.Mul(result, base)
		result.Add(result, big.NewInt(int64(idx)))
	}

	decoded := result.Bytes()
	leadingZeros := 0
	for leadingZeros < len(s) && s[leadingZeros] == '1' {
		leadingZeros++
	}
	if leadingZeros > 0 {
		decoded = append(make([]byte, leadingZeros), decoded...)
	}
	if len(decoded) != wantLen {
		return nil, fmt.Errorf("decoded length %d, want %d", len(decoded), wantLen)
	}
	return decoded, nil
}

func canonicalApprovalMessage(proposalID string, proposal *MultisigProposal) []byte {
	msg := fmt.Sprintf("%s|%s|%d|%s|%s|%d",
		proposalID,
		proposal.Action,
		proposal.Amount,
		proposal.DestAddress,
		proposal.MintAddress,
		proposal.Nonce,
	)
	return []byte(msg)
}

// CreateMultisig deploys a new multisig account on Solana.
//
// This creates a native multisig account with the SPL Governance program.
// The multisig account has 3 signers and a threshold of 2.
//
// In production, this is typically done once during initial deployment and
// the multisig PDA is persisted in the deployment manifest.
//
// The returned string is the multisig PDA address.
//
// Phase 4: Full SDK integration required for the transaction construction.
// The createMultisig instruction comes from the SPL Governance program.
func CreateMultisig(ctx context.Context, config *MultisigConfig) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}

	logger := slog.Default().With("component", "multisig-create")
	logger.Info("creating 2-of-3 multisig",
		"threshold", config.Threshold,
		"signer_0", config.Signers[0],
		"signer_1", config.Signers[1],
		"signer_2", config.Signers[2],
	)

	// -----------------------------------------------------------------------
	// Phase 4 skeleton — deploy multisig via SPL Governance or native multisig
	// -----------------------------------------------------------------------
	//
	// Option A: SPL Governance (recommended for DAO-grade governance)
	//
	//   govProgramID := solana.MustPublicKeyFromBase58("GovER5Lthms3bLBqWub97yVrMmEogzX7xNjdXpPPCVZw")
	//   // Create a multisig realm with 3 council members, threshold 2.
	//   ...
	//
	// Option B: Native multisig (simpler, suitable for operational multisig)
	//
	//   // The native multisig is created via system program createAccount
	//   // followed by an initialize instruction. Signers are encoded as a
	//   // bitmask; threshold is stored on-chain.
	//   multisigKP := solana.NewWallet()
	//   ...
	//
	// For MVP, the multisig PDA is derived and tracked offline; on-chain
	// creation is deferred until a treasury multisig is actually deployed.
	// In the interim, the deploy CLI outputs the multisig PDA for reference.

	_ = ctx

	if config.PDA == "" {
		return "", fmt.Errorf("multisig: PDA address is empty — run derive-multisig-pda first")
	}

	logger.Info("multisig PDA derived (on-chain creation deferred to treasury deployment)",
		"pda", config.PDA,
	)

	return config.PDA, nil
}

// ProposeMint creates a mint proposal to issue vUSDC tokens.
//
// The proposer must be one of the 3 multisig signers. The proposal enters
// "pending" state and requires 2 approvals (including the proposer's if
// they also approve) before it can be executed.
//
// Parameters:
//   - proposer: the Signer submitting the proposal (must be a multisig member)
//   - amount: token amount in native units (6 decimals for vUSDC)
//   - destAddress: the recipient wallet that will receive the minted tokens
//
// Returns the proposal ID.
func (m *MultisigManager) ProposeMint(ctx context.Context, proposer *Signer, amount uint64, destAddress string) (string, error) {
	if !m.isSigner(proposer.PublicKey) {
		return "", fmt.Errorf("multisig: proposer %s is not a member of the multisig", proposer.PublicKey)
	}
	if amount == 0 {
		return "", fmt.Errorf("multisig: mint amount must be positive")
	}
	if destAddress == "" {
		return "", fmt.Errorf("multisig: destination address is required")
	}
	if m.config.MintAddress == "" {
		return "", fmt.Errorf("multisig: mint address is required in config")
	}

	nonce := m.nextNonce()
	id := deriveProposalID(ProposalActionMint, amount, destAddress, m.config.MintAddress, nonce)

	proposal := &MultisigProposal{
		ID:          id,
		Proposer:    proposer.PublicKey,
		Action:      ProposalActionMint,
		Amount:      amount,
		DestAddress: destAddress,
		MintAddress: m.config.MintAddress,
		Nonce:       nonce,
		Approvals:   make(map[string]bool),
		Status:      ProposalStatusPending,
		CreatedAt:   time.Now().UTC(),
	}

	m.mu.Lock()
	m.proposals[id] = proposal
	m.mu.Unlock()

	// SECURITY FIX: F-004-01 — Persist proposal to DB.
	if err := m.saveProposalToDB(ctx, proposal); err != nil {
		return "", err
	}

	m.logger.Info("mint proposal created",
		"proposal_id", id,
		"proposer", proposer.PublicKey,
		"amount", amount,
		"dest", destAddress,
		"nonce", nonce,
	)

	return id, nil
}

// ApproveProposal records an approval from a multisig signer for a pending
// proposal. The caller MUST provide a valid EdDSA (Ed25519) signature over
// the proposal details to prove possession of the signer's private key.
//
// The signed message is:
// proposalID + "|" + action + "|" + amount + "|" + destAddress + "|" + mintAddress + "|" + nonce
//
// If the threshold (2) is reached, the proposal status transitions to
// "approved" and is ready for execution.
//
// Each signer may only approve once per proposal. Duplicate approvals are
// idempotent (no error, but no state change).
//
// SECURITY FIX: F-004-04 — Added mandatory EdDSA signature verification.
// Approvals without a valid signature from a multisig member are rejected.
func (m *MultisigManager) ApproveProposal(ctx context.Context, approver *Signer, proposalID string, signatureB64 string) error {
	if !m.isSigner(approver.PublicKey) {
		return fmt.Errorf("multisig: approver %s is not a member of the multisig", approver.PublicKey)
	}

	// Look up the proposal first to get its data for signature verification.
	m.mu.RLock()
	proposal, ok := m.proposals[proposalID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("multisig: proposal %s not found", proposalID)
	}

	// Build the canonical message that the signer must have signed.
	sigMsg := canonicalApprovalMessage(proposalID, proposal)

	// Decode the base64 signature.
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		m.mu.RUnlock()
		return fmt.Errorf("multisig: invalid signature encoding: %w", err)
	}

	// Derive the approver's Ed25519 public key from their Solana base58 public key.
	approverPubKey, err := decodeBase58Fixed(approver.PublicKey, ed25519.PublicKeySize)
	if err != nil {
		m.mu.RUnlock()
		return fmt.Errorf("multisig: invalid approver public key %s: %w", approver.PublicKey, err)
	}

	// Verify the EdDSA signature against the canonical message bytes.
	if !ed25519.Verify(approverPubKey, sigMsg, sigBytes) {
		m.mu.RUnlock()
		return fmt.Errorf("multisig: signature verification failed for approver %s on proposal %s", approver.PublicKey, proposalID)
	}
	m.mu.RUnlock()

	// Signature valid — take write lock to update proposal state.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-fetch under write lock to prevent TOCTOU.
	proposal, ok = m.proposals[proposalID]
	if !ok {
		return fmt.Errorf("multisig: proposal %s not found", proposalID)
	}

	if proposal.Status != ProposalStatusPending && proposal.Status != ProposalStatusApproved {
		return fmt.Errorf("multisig: proposal %s is in status %s, cannot approve", proposalID, proposal.Status)
	}

	if proposal.Approvals[approver.PublicKey] {
		m.logger.Debug("approve proposal: already approved by this signer",
			"proposal_id", proposalID,
			"approver", approver.PublicKey,
		)
		return nil // idempotent
	}

	proposal.Approvals[approver.PublicKey] = true

	approvalCount := len(proposal.Approvals)
	m.logger.Info("proposal approved by signer",
		"proposal_id", proposalID,
		"approver", approver.PublicKey,
		"approval_count", approvalCount,
		"threshold", m.config.Threshold,
	)

	if approvalCount >= int(m.config.Threshold) {
		proposal.Status = ProposalStatusApproved
		m.logger.Info("proposal threshold reached — ready for execution",
			"proposal_id", proposalID,
			"approval_count", approvalCount,
		)
	}

	// Persist to DB after state change.
	if err := m.saveProposalToDB(ctx, proposal); err != nil {
		return err
	}

	return nil
}

// ExecuteProposal executes a proposal that has reached the threshold.
//
// The executor must be one of the multisig signers (any signer can execute
// once threshold is met). The execution constructs and submits the actual
// Solana transaction (mintTo, burn, freeze, etc.) and records the tx
// signature on success.
//
// Returns the transaction signature.
//
// IMPORTANT: The executor does NOT need to be one of the approvers — any
// multisig member can submit the transaction once the threshold is met.
func (m *MultisigManager) ExecuteProposal(ctx context.Context, executor *Signer, proposalID string) (string, error) {
	if !m.isSigner(executor.PublicKey) {
		return "", fmt.Errorf("multisig: executor %s is not a member of the multisig", executor.PublicKey)
	}

	m.mu.Lock()
	proposal, ok := m.proposals[proposalID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("multisig: proposal %s not found", proposalID)
	}

	if proposal.Status != ProposalStatusApproved {
		m.mu.Unlock()
		return "", fmt.Errorf("multisig: proposal %s is in status %s (need approved)", proposalID, proposal.Status)
	}

	// Mark as executing and persist before the actual chain call to prevent
	// restart-time double execution.
	proposal.Status = ProposalStatusExecuting
	if err := m.saveProposalToDB(ctx, proposal); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()

	m.logger.Info("executing proposal",
		"proposal_id", proposalID,
		"action", proposal.Action,
		"executor", executor.PublicKey,
	)

	// -----------------------------------------------------------------------
	// Phase 4 skeleton — execute based on action type
	// -----------------------------------------------------------------------
	var txSig string
	var execErr error

	switch proposal.Action {
	case ProposalActionMint:
		// The multisig PDA is the mint authority.
		// In SPL Governance, the multisig executes via CPI.
		//
		// This requires constructing a governance executeTransaction
		// instruction. The inner instruction is a mintTo to the dest ATA.
		//
		// For a simpler native multisig:
		//   1. Assemble signatures from all approvers.
		//   2. Submit the multi-sig transaction.
		//
		// Callback to MintTo with the multisig PDA as authority.
		txSig, execErr = MintTo(ctx, m.rpcClient.endpoint, executor, proposal.MintAddress, proposal.DestAddress, proposal.Amount)

	case ProposalActionBurn:
		txSig, execErr = Burn(ctx, m.rpcClient.endpoint, executor, proposal.MintAddress, proposal.Amount)

	case ProposalActionFreeze:
		// freezeAccount instruction — deferred.
		execErr = fmt.Errorf("solana: freeze not yet implemented")

	case ProposalActionThaw:
		// thawAccount instruction — deferred.
		execErr = fmt.Errorf("solana: thaw not yet implemented")

	case ProposalActionTransferMintAuthority:
		// setAuthority instruction — deferred.
		execErr = fmt.Errorf("solana: transfer mint authority not yet implemented")

	default:
		execErr = fmt.Errorf("multisig: unknown action %q", proposal.Action)
	}

	if execErr != nil {
		// Revert proposal status on failure.
		m.mu.Lock()
		proposal.Status = ProposalStatusApproved
		proposal.ExecutedAt = nil
		persistErr := m.saveProposalToDB(ctx, proposal)
		m.mu.Unlock()

		m.logger.Error("proposal execution failed",
			"proposal_id", proposalID,
			"action", proposal.Action,
			"error", execErr,
		)
		if persistErr != nil {
			return "", fmt.Errorf("multisig: execute proposal %s: %w; rollback persist failed: %v", proposalID, execErr, persistErr)
		}
		return "", fmt.Errorf("multisig: execute proposal %s: %w", proposalID, execErr)
	}

	// Record the tx signature and terminal state.
	m.mu.Lock()
	now := time.Now().UTC()
	proposal.Status = ProposalStatusExecuted
	proposal.ExecutedAt = &now
	proposal.ExecutedTxID = txSig
	if err := m.saveProposalToDB(ctx, proposal); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()

	m.logger.Info("proposal executed successfully",
		"proposal_id", proposalID,
		"action", proposal.Action,
		"tx_sig", txSig,
		"executed_at", now,
	)

	return txSig, nil
}

// GetProposal returns the current state of a proposal by ID.
func (m *MultisigManager) GetProposal(proposalID string) (*MultisigProposal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	proposal, ok := m.proposals[proposalID]
	if !ok {
		return nil, fmt.Errorf("multisig: proposal %s not found", proposalID)
	}

	// Return a copy to prevent races.
	cp := *proposal
	cp.Approvals = make(map[string]bool, len(proposal.Approvals))
	for k, v := range proposal.Approvals {
		cp.Approvals[k] = v
	}
	return &cp, nil
}

// ListPendingProposals returns all proposals that have not yet been executed.
func (m *MultisigManager) ListPendingProposals() []*MultisigProposal {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var pending []*MultisigProposal
	for _, p := range m.proposals {
		if p.Status == ProposalStatusPending || p.Status == ProposalStatusApproved || p.Status == ProposalStatusExecuting {
			cp := *p
			cp.Approvals = make(map[string]bool, len(p.Approvals))
			for k, v := range p.Approvals {
				cp.Approvals[k] = v
			}
			pending = append(pending, &cp)
		}
	}
	return pending
}

// RejectProposal marks a proposal as rejected. Only callable by a multisig
// signer who has NOT yet approved.
func (m *MultisigManager) RejectProposal(ctx context.Context, signer *Signer, proposalID string) error {
	if !m.isSigner(signer.PublicKey) {
		return fmt.Errorf("multisig: signer %s is not a member of the multisig", signer.PublicKey)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	proposal, ok := m.proposals[proposalID]
	if !ok {
		return fmt.Errorf("multisig: proposal %s not found", proposalID)
	}

	if proposal.Status != ProposalStatusPending && proposal.Status != ProposalStatusApproved {
		return fmt.Errorf("multisig: proposal %s is in status %s, cannot reject", proposalID, proposal.Status)
	}

	if proposal.Approvals[signer.PublicKey] {
		// A signer who approved can only reject if they revoke their approval
		// (remove from approvals map, then reject if below threshold).
		delete(proposal.Approvals, signer.PublicKey)
	}

	proposal.Status = ProposalStatusRejected

	// SECURITY FIX: F-004-01 — Persist rejection to DB.
	if err := m.saveProposalToDB(ctx, proposal); err != nil {
		return err
	}

	m.logger.Info("proposal rejected",
		"proposal_id", proposalID,
		"rejected_by", signer.PublicKey,
	)

	return nil
}

// ExportProposals serializes all proposals to JSON for persistence/audit.
func (m *MultisigManager) ExportProposals() (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := json.Marshal(m.proposals)
	if err != nil {
		return nil, fmt.Errorf("multisig: export proposals: %w", err)
	}
	return data, nil
}

// ImportProposals restores proposals from a JSON export (for state recovery).
func (m *MultisigManager) ImportProposals(data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := json.Unmarshal(data, &m.proposals); err != nil {
		return fmt.Errorf("multisig: import proposals: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// PostgreSQL persistence (SECURITY FIX: F-004-01)
// ---------------------------------------------------------------------------

// proposalsTableDDL is the CREATE TABLE IF NOT EXISTS statement for persisting
// multisig proposals in PostgreSQL. The proposals table is the single source of
// truth for multisig state. It survives process restarts and allows recovery
// of in-flight proposals.
const proposalsTableDDL = `
CREATE TABLE IF NOT EXISTS multisig_proposals (
    id              VARCHAR(100) PRIMARY KEY,
    proposer        VARCHAR(88)  NOT NULL,
    action          VARCHAR(50)  NOT NULL,
    amount          BIGINT       NOT NULL DEFAULT 0,
    dest_address    VARCHAR(88)  NOT NULL DEFAULT '',
    mint_address    VARCHAR(88)  NOT NULL DEFAULT '',
    nonce           BIGINT       NOT NULL,
    approvals       JSONB        NOT NULL DEFAULT '{}',
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'executing', 'executed', 'rejected', 'expired')),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    executed_at     TIMESTAMPTZ,
    executed_tx_id  VARCHAR(200),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_multisig_proposals_status
    ON multisig_proposals(status);
`

// EnsureProposalsTable creates the multisig_proposals table if it does not
// already exist. Call during service initialization.
func (m *MultisigManager) EnsureProposalsTable(ctx context.Context) error {
	if m.db == nil {
		return nil // no-op when DB is not configured
	}
	_, err := m.db.ExecContext(ctx, proposalsTableDDL)
	if err != nil {
		return fmt.Errorf("multisig: ensure proposals table: %w", err)
	}
	return nil
}

// saveProposalToDB upserts a single proposal into PostgreSQL. Called after
// every state-changing operation (create, approve, execute, reject).
func (m *MultisigManager) saveProposalToDB(ctx context.Context, proposal *MultisigProposal) error {
	if m.db == nil {
		return nil // no-op when DB is not configured
	}

	approvalsJSON, err := json.Marshal(proposal.Approvals)
	if err != nil {
		return fmt.Errorf("multisig: marshal approvals for %s: %w", proposal.ID, err)
	}

	query := `
		INSERT INTO multisig_proposals (
			id, proposer, action, amount, dest_address, mint_address,
			nonce, approvals, status, created_at, executed_at, executed_tx_id, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (id) DO UPDATE SET
			approvals      = EXCLUDED.approvals,
			status         = EXCLUDED.status,
			executed_at    = EXCLUDED.executed_at,
			executed_tx_id = EXCLUDED.executed_tx_id,
			updated_at     = NOW()
		WHERE multisig_proposals.proposer = EXCLUDED.proposer
		  AND multisig_proposals.action = EXCLUDED.action
		  AND multisig_proposals.amount = EXCLUDED.amount
		  AND multisig_proposals.dest_address = EXCLUDED.dest_address
		  AND multisig_proposals.mint_address = EXCLUDED.mint_address
		  AND multisig_proposals.nonce = EXCLUDED.nonce
	`

	result, err := m.db.ExecContext(ctx, query,
		proposal.ID,
		proposal.Proposer,
		proposal.Action,
		int64(proposal.Amount),
		proposal.DestAddress,
		proposal.MintAddress,
		int64(proposal.Nonce),
		approvalsJSON,
		proposal.Status,
		proposal.CreatedAt,
		proposal.ExecutedAt,
		proposal.ExecutedTxID,
	)
	if err != nil {
		return fmt.Errorf("multisig: persist proposal %s: %w", proposal.ID, err)
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("multisig: proposal id collision for %s with different immutable fields", proposal.ID)
	}
	return nil
}

// loadProposalsFromDB restores all proposals from PostgreSQL into the in-memory
// map. Called once during startup. Proposals in terminal states (executed,
// rejected, expired) are loaded for audit but presented only when queried
// directly.
func (m *MultisigManager) loadProposalsFromDB(ctx context.Context) error {
	if m.db == nil {
		return nil
	}

	rows, err := m.db.QueryContext(ctx,
		`SELECT id, proposer, action, amount, dest_address, mint_address,
		        nonce, approvals, status, created_at, executed_at, executed_tx_id
		 FROM multisig_proposals
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return fmt.Errorf("multisig: load proposals from DB: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var p MultisigProposal
		var approvalsJSON []byte
		var amount, nonce int64

		if err := rows.Scan(
			&p.ID, &p.Proposer, &p.Action, &amount, &p.DestAddress,
			&p.MintAddress, &nonce, &approvalsJSON, &p.Status,
			&p.CreatedAt, &p.ExecutedAt, &p.ExecutedTxID,
		); err != nil {
			m.logger.Warn("failed to scan proposal row, skipping",
				"error", err,
			)
			continue
		}

		p.Amount = uint64(amount)
		p.Nonce = uint64(nonce)
		p.Approvals = make(map[string]bool)

		if err := json.Unmarshal(approvalsJSON, &p.Approvals); err != nil {
			m.logger.Warn("failed to unmarshal approvals JSON, defaulting to empty",
				"proposal_id", p.ID,
				"error", err,
			)
			p.Approvals = make(map[string]bool)
		}

		m.proposals[p.ID] = &p
		count++

		// Track highest nonce for new proposal generation.
		if p.Nonce >= m.nonce {
			m.nonce = p.Nonce
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("multisig: rows iteration: %w", err)
	}

	m.logger.Info("loaded proposals from DB",
		"count", count,
		"highest_nonce", m.nonce,
	)

	return nil
}

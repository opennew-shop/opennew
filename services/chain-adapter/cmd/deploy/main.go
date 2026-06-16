// Command deploy is a CLI tool for deploying the vUSDC Token-2022 mint and
// its associated 2-of-3 multisig on Solana.
//
// Usage:
//
//	go run cmd/deploy/main.go \
//	    --rpc devnet \
//	    --keypair wallet.json \
//	    --output deploy-output.json
//
// The tool performs the following steps:
//
//  1. Load the payer keypair from the provided JSON file.
//  2. Connect to the Solana RPC endpoint (devnet, testnet, or mainnet).
//  3. Derive the 2-of-3 multisig PDA from the three signer public keys.
//  4. Deploy the Token-2022 mint with multisig PDA as mint authority.
//  5. Optionally set freeze authority (requires --freeze-keypair).
//  6. Write the deployment manifest (mint address, multisig PDA, signers,
//     reserve addresses) to stdout and optionally to an output file.
//
// Phase 4: The actual on-chain deployment requires full Solana SDK
// integration. This CLI prints the derived addresses and configuration
// so operators can verify before deploying with a Solana-compatible tool.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ancf-commerce/ancf/services/chain-adapter/internal/solana"
)

// DeployConfig holds the configuration for the vUSDC deployment CLI.
type DeployConfig struct {
	RPCURL        string   `json:"rpc_url"`
	Network       string   `json:"network"`
	DryRun        bool     `json:"dry_run"`
	KeypairFile   string   `json:"keypair_file"`
	SignerFiles   []string `json:"signer_files"`
	OutputFile    string   `json:"output_file"`
	FreezeKeypair string   `json:"freeze_keypair,omitempty"`
}

// DeployOutput is the result of a deployment, written to stdout and optionally
// to an output JSON file for downstream consumption.
type DeployOutput struct {
	ProtocolVersion string        `json:"protocol_version"`
	DeployedAt      string        `json:"deployed_at"`
	Network         string        `json:"network"`
	RPCURL          string        `json:"rpc_url"`
	Mint            *MintInfo     `json:"mint"`
	Multisig        *MultisigInfo `json:"multisig"`
	Reserve         *ReserveInfo  `json:"reserve"`
	Payer           string        `json:"payer"`
	DryRun          bool          `json:"dry_run"`
	Warnings        []string      `json:"warnings,omitempty"`
}

// MintInfo describes the deployed vUSDC Token-2022 mint.
type MintInfo struct {
	Address         string `json:"address"`
	Decimals        uint8  `json:"decimals"`
	MintAuthority   string `json:"mint_authority"`
	FreezeAuthority string `json:"freeze_authority,omitempty"`
	IsDeployed      bool   `json:"is_deployed"`
	TxSignature     string `json:"tx_signature,omitempty"`
}

// MultisigInfo describes the 2-of-3 multisig.
type MultisigInfo struct {
	Threshold  uint8    `json:"threshold"`
	Signers    []string `json:"signers"`
	PDA        string   `json:"pda"`
	IsDeployed bool     `json:"is_deployed"`
}

// ReserveInfo describes the reserve USDC wallet.
type ReserveInfo struct {
	Address string `json:"address"`
}

// 各 Solana 环境的默认 RPC 端点。
const (
	defaultDevnetRPC  = "https://api.devnet.solana.com"
	defaultTestnetRPC = "https://api.testnet.solana.com"
	defaultMainnetRPC = "https://api.mainnet-beta.solana.com"
)

// main 解析命令行参数，派生多签 PDA 与 mint 地址，
// 输出部署清单（JSON），并打印后续操作步骤。
func main() {
	rpcFlag := flag.String("rpc", "devnet",
		"Solana RPC environment: devnet, testnet, mainnet, or a full URL")
	keypairFile := flag.String("keypair", "",
		"Path to payer keypair JSON file (required for real deployment)")
	signersFlag := flag.String("signers", "",
		"Comma-separated paths to the 3 signer keypair files")
	dryRun := flag.Bool("dry-run", false,
		"Print derived addresses without deploying (default: false)")
	outputFile := flag.String("output", "",
		"Path to output JSON file (default: stdout only)")
	freezeKeypair := flag.String("freeze-keypair", "",
		"Path to freeze authority keypair (optional)")
	mintAddressFlag := flag.String("mint", "",
		"Existing mint address (skip deployment, print reconciliation info)")
	printConfig := flag.Bool("print-config", false,
		"Print the vUSDC reconciler configuration as environment variables")

	flag.Parse()

	rpcURL := resolveRPCURL(*rpcFlag)
	network := resolveNetwork(*rpcFlag)

	fmt.Fprintf(os.Stderr, "=== ANCF vUSDC Token-2022 Deployment Tool ===\n")
	fmt.Fprintf(os.Stderr, "Network:  %s\n", network)
	fmt.Fprintf(os.Stderr, "RPC URL:  %s\n", rpcURL)
	fmt.Fprintf(os.Stderr, "Dry run:  %v\n", *dryRun)
	fmt.Fprintf(os.Stderr, "=============================================\n\n")

	if *printConfig {
		printEnvConfig(rpcURL, network)
		return
	}

	var payer *solana.Signer
	if *keypairFile != "" {
		p, err := loadKeypair(*keypairFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading payer keypair: %v\n", err)
			os.Exit(1)
		}
		payer = p
	}

	signerFiles := parseSignerFiles(*signersFlag)
	var signerPubkeys [3]string
	if len(signerFiles) > 0 {
		if len(signerFiles) != 3 {
			fmt.Fprintf(os.Stderr, "Error: exactly 3 signer keypairs required for 2-of-3 multisig, got %d\n",
				len(signerFiles))
			os.Exit(1)
		}
		for i, sf := range signerFiles {
			s, err := loadKeypair(sf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading signer %d keypair from %s: %v\n", i+1, sf, err)
				os.Exit(1)
			}
			signerPubkeys[i] = s.PublicKey
		}
	} else {
		fmt.Fprintf(os.Stderr, "Warning: no signer keypairs provided. Using placeholder public keys.\n")
		fmt.Fprintf(os.Stderr, "         Run with --signers s1.json,s2.json,s3.json for real keys.\n\n")
		signerPubkeys = [3]string{
			"SIGNER_1_PUBKEY_PLACEHOLDER",
			"SIGNER_2_PUBKEY_PLACEHOLDER",
			"SIGNER_3_PUBKEY_PLACEHOLDER",
		}
	}

	multisigPDA := deriveMultisigPDA(signerPubkeys)

	multisigConfig := &solana.MultisigConfig{
		Threshold: 2,
		Signers:   signerPubkeys,
		PDA:       multisigPDA,
	}
	_ = multisigConfig // used when deploying via SDK

	mintAddress := *mintAddressFlag
	if mintAddress == "" {
		mintAddress = deriveMintAddress(multisigPDA)
	}

	var txSig string
	isDeployed := false

	if !*dryRun {
		if payer == nil {
			fmt.Fprintf(os.Stderr, "Error: --keypair is required for real deployment (use --dry-run for address derivation)\n")
			os.Exit(1)
		}

		freezeAuth := ""
		if *freezeKeypair != "" {
			freezeSigner, err := loadKeypair(*freezeKeypair)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading freeze keypair: %v\n", err)
				os.Exit(1)
			}
			freezeAuth = freezeSigner.PublicKey
		}

		fmt.Fprintf(os.Stderr, "Deploying vUSDC Token-2022 mint...\n")
		fmt.Fprintf(os.Stderr, "  Payer:           %s\n", payer.PublicKey)
		fmt.Fprintf(os.Stderr, "  Mint address:    %s\n", mintAddress)
		fmt.Fprintf(os.Stderr, "  Mint authority:  %s (multisig PDA)\n", multisigPDA)
		if freezeAuth != "" {
			fmt.Fprintf(os.Stderr, "  Freeze authority: %s\n", freezeAuth)
		} else {
			fmt.Fprintf(os.Stderr, "  Freeze authority: none\n")
		}
		fmt.Fprintf(os.Stderr, "\n")

		fmt.Fprintf(os.Stderr, "Phase 4: Full Solana SDK integration required for on-chain deployment.\n")
		fmt.Fprintf(os.Stderr, "         Run with --dry-run to see derived addresses.\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Dry run — derived addresses (no on-chain deployment):\n")
	}

	freezeAuth := ""
	if *freezeKeypair != "" {
		s, err := loadKeypair(*freezeKeypair)
		if err == nil {
			freezeAuth = s.PublicKey
		}
	}

	warnings := generateWarnings(*dryRun, signerPubkeys, multisigPDA)

	output := DeployOutput{
		ProtocolVersion: "ANCF-1.0",
		DeployedAt:      time.Now().UTC().Format(time.RFC3339),
		Network:         network,
		RPCURL:          rpcURL,
		Mint: &MintInfo{
			Address:         mintAddress,
			Decimals:        solana.DefaultVUSDCDecimals,
			MintAuthority:   multisigPDA,
			FreezeAuthority: freezeAuth,
			IsDeployed:      isDeployed,
			TxSignature:     txSig,
		},
		Multisig: &MultisigInfo{
			Threshold:  2,
			Signers:    []string{signerPubkeys[0], signerPubkeys[1], signerPubkeys[2]},
			PDA:        multisigPDA,
			IsDeployed: false,
		},
		Reserve: &ReserveInfo{
			Address: "RESERVE_USDC_ADDRESS_PLACEHOLDER",
		},
		Payer: func() string {
			if payer != nil {
				return payer.PublicKey
			}
			return "PAYER_NOT_PROVIDED"
		}(),
		DryRun:  *dryRun,
		Warnings: warnings,
	}

	outputBytes, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(outputBytes))

	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, outputBytes, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\nOutput written to: %s\n", *outputFile)
	}

	fmt.Fprintf(os.Stderr, "\n=== Next Steps ===\n")
	fmt.Fprintf(os.Stderr, "1. Verify the multisig PDA and signer addresses.\n")
	fmt.Fprintf(os.Stderr, "2. Fund the payer account with SOL for deployment gas.\n")
	if *dryRun {
		fmt.Fprintf(os.Stderr, "3. Re-run without --dry-run to deploy on-chain.\n")
	}
	fmt.Fprintf(os.Stderr, "4. Set environment variables:\n")
	fmt.Fprintf(os.Stderr, "   export VUSDC_MINT_ADDRESS=%s\n", mintAddress)
	fmt.Fprintf(os.Stderr, "   export VUSDC_MULTISIG_PDA=%s\n", multisigPDA)
	fmt.Fprintf(os.Stderr, "5. Update the chain-adapter configuration.\n")
	fmt.Fprintf(os.Stderr, "6. Fund the reserve USDC address before enabling deposits.\n")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveRPCURL 将 --rpc 参数（devnet/testnet/mainnet 或完整 URL）解析为实际 RPC 端点。
func resolveRPCURL(flag string) string {
	switch flag {
	case "devnet":
		return defaultDevnetRPC
	case "testnet":
		return defaultTestnetRPC
	case "mainnet", "mainnet-beta":
		return defaultMainnetRPC
	default:
		return flag
	}
}

// resolveNetwork 将 --rpc 参数映射为对应的网络标识（如 solana-mainnet）。
func resolveNetwork(flag string) string {
	switch flag {
	case "devnet":
		return "solana-devnet"
	case "testnet":
		return "solana-testnet"
	case "mainnet", "mainnet-beta":
		return "solana-mainnet"
	default:
		return "solana-custom"
	}
}

// loadKeypair 从 JSON 文件读取 Solana 密钥对（[u8; 64] 数组），
// 拆分出后 32 字节公钥与前 32 字节私钥并以十六进制返回。
func loadKeypair(path string) (*solana.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keypair file: %w", err)
	}

	var keyBytes []byte
	if err := json.Unmarshal(data, &keyBytes); err != nil {
		return nil, fmt.Errorf("parse keypair JSON: %w (expected [u8; 64] array)", err)
	}
	if len(keyBytes) != 64 {
		return nil, fmt.Errorf("invalid keypair: expected 64 bytes, got %d", len(keyBytes))
	}

	privateKey := keyBytes[:32]
	publicKey := keyBytes[32:]

	return &solana.Signer{
		PublicKey:  fmt.Sprintf("%x", publicKey),
		PrivateKey: fmt.Sprintf("%x", privateKey),
	}, nil
}

// deriveMultisigPDA 由 3 个签名者公钥与阈值 2 确定性派生 2-of-3 多签 PDA 地址。
func deriveMultisigPDA(signers [3]string) string {
	payload := fmt.Sprintf("multisig|%s|%s|%s|2", signers[0], signers[1], signers[2])
	hash := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("MULTISIG_PDA_%x", hash[:20])
}

// deriveMintAddress 由多签 PDA 确定性派生 vUSDC mint 地址。
func deriveMintAddress(multisigPDA string) string {
	payload := fmt.Sprintf("vusdc_mint|%s", multisigPDA)
	hash := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("VUSDC_MINT_%x", hash[:20])
}

// generateWarnings 根据是否 dry-run、是否使用占位签名者及 PDA 是否为空，
// 生成部署前的告警提示列表。
func generateWarnings(dryRun bool, signers [3]string, pda string) []string {
	var warnings []string
	if dryRun {
		warnings = append(warnings,
			"This is a dry run. No on-chain transactions were submitted.",
			"Signer addresses are placeholders unless --signers flag was used.",
		)
	}
	for i, s := range signers {
		expected := fmt.Sprintf("SIGNER_%d_PUBKEY_PLACEHOLDER", i+1)
		if s == expected {
			warnings = append(warnings,
				fmt.Sprintf("Signer %d uses a placeholder public key. Replace with a real key before deployment.", i+1))
		}
	}
	if pda == "" {
		warnings = append(warnings, "Multisig PDA is empty. Deployment will fail.")
	}
	return warnings
}

// parseSignerFiles 将逗号分隔的签名者密钥文件路径拆分为去空白后的切片。
func parseSignerFiles(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// printEnvConfig 以环境变量形式打印 vUSDC 对账器配置，便于写入 .env 文件。
func printEnvConfig(rpcURL string, network string) {
	fmt.Println("# ANCF vUSDC Token-2022 Configuration")
	fmt.Println("# Add these to your chain-adapter .env file")
	fmt.Println()
	fmt.Printf("SOLANA_RPC_ENDPOINT=%s\n", rpcURL)
	fmt.Printf("SOLANA_NETWORK=%s\n", network)
	fmt.Println()
	fmt.Println("# vUSDC Token-2022 Mint (populate after deployment)")
	fmt.Println("# VUSDC_MINT_ADDRESS=")
	fmt.Println("# VUSDC_MULTISIG_PDA=")
	fmt.Println("# VUSDC_RESERVE_ADDRESS=")
	fmt.Println()
	fmt.Println("# Reconciliation config")
	fmt.Println("# VUSDC_RECONCILIATION_INTERVAL=15m")
	fmt.Println("# VUSDC_RECONCILIATION_TOLERANCE_PCT=1.0")
}

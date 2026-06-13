/**
 * ANCF AGP Token-2022 Devnet Mint
 * Uses @solana/web3.js + @solana/spl-token
 * Destination: Giqt4TrXHzkPBYSD4Rs9K9VB6BVqznsWnmyPgVJKdhDw
 */
const {
  Connection, Keypair, PublicKey, clusterApiUrl,
  LAMPORTS_PER_SOL, sendAndConfirmTransaction, Transaction,
  SystemProgram,
} = require('@solana/web3.js');
const {
  createInitializeMint2Instruction,
  getMinimumBalanceForRentExemptMint,
  MINT_SIZE,
  TOKEN_2022_PROGRAM_ID,
  createMintToInstruction,
  getOrCreateAssociatedTokenAccount,
} = require('@solana/spl-token');
const fs = require('fs');
const path = require('path');

// ─── Config ───
const DESTINATION = 'Giqt4TrXHzkPBYSD4Rs9K9VB6BVqznsWnmyPgVJKdhDw';
const DECIMALS = 6;
const MINT_AMOUNT = 1_000_000_000_000n; // 1M AGP (in native units)
const RPC_URL = clusterApiUrl('devnet');
const PAYER_FILE = path.join(__dirname, 'payer.json');

async function main() {
  console.log('╔══════════════════════════════════════════════╗');
  console.log('║  ANCF AGP Token-2022 Devnet Mint (Node.js) ║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  RPC:    ', RPC_URL);
  console.log('║  Dest:   ', DESTINATION);
  console.log('║  Amount:  1,000,000.000000 AGP');
  console.log('╚══════════════════════════════════════════════╝\n');

  // ── Step 1: Connect ──
  const conn = new Connection(RPC_URL, 'confirmed');
  const version = await conn.getVersion();
  console.log('[1/5] Connected. Solana version:', version['solana-core']);

  // ── Step 2: Load or generate payer ──
  let payer;
  if (fs.existsSync(PAYER_FILE)) {
    const raw = JSON.parse(fs.readFileSync(PAYER_FILE, 'utf-8'));
    payer = Keypair.fromSecretKey(Uint8Array.from(raw));
    console.log('[2/5] Loaded payer:', payer.publicKey.toBase58());
  } else {
    payer = Keypair.generate();
    fs.writeFileSync(PAYER_FILE, JSON.stringify(Array.from(payer.secretKey)));
    console.log('[2/5] Generated new payer:', payer.publicKey.toBase58());
    console.log('      Keypair saved to:', PAYER_FILE);
  }

  // Check balance
  let balance = await conn.getBalance(payer.publicKey);
  console.log('      Balance:', (balance / LAMPORTS_PER_SOL).toFixed(6), 'SOL');

  // Airdrop if needed
  if (balance < 0.05 * LAMPORTS_PER_SOL) {
    console.log('      Requesting airdrop of 2 SOL...');
    const sig = await conn.requestAirdrop(payer.publicKey, 2 * LAMPORTS_PER_SOL);
    await conn.confirmTransaction(sig, 'confirmed');
    balance = await conn.getBalance(payer.publicKey);
    console.log('      New balance:', (balance / LAMPORTS_PER_SOL).toFixed(6), 'SOL');
  }

  // ── Step 3: Create Token-2022 Mint ──
  console.log('\n[3/5] Creating Token-2022 Mint...');
  const mintKeypair = Keypair.generate();
  const mintPubkey = mintKeypair.publicKey;
  console.log('      Mint:', mintPubkey.toBase58());

  const lamports = await getMinimumBalanceForRentExemptMint(conn);

  const createMintTx = new Transaction().add(
    SystemProgram.createAccount({
      fromPubkey: payer.publicKey,
      newAccountPubkey: mintPubkey,
      space: MINT_SIZE,
      lamports,
      programId: TOKEN_2022_PROGRAM_ID,
    }),
    createInitializeMint2Instruction(
      mintPubkey,
      DECIMALS,
      payer.publicKey,  // mint authority
      null,             // freeze authority = none
      TOKEN_2022_PROGRAM_ID
    )
  );

  const mintSig = await sendAndConfirmTransaction(conn, createMintTx, [payer, mintKeypair]);
  console.log('      Mint created! tx:', mintSig);

  // ── Step 4: Create ATA + Mint tokens ──
  console.log('\n[4/5] Creating ATA for destination...');
  const destPubkey = new PublicKey(DESTINATION);

  const ata = await getOrCreateAssociatedTokenAccount(
    conn,
    payer,
    mintPubkey,
    destPubkey,
    false, // allowOwnerOffCurve
    'confirmed',
    { commitment: 'confirmed' },
    TOKEN_2022_PROGRAM_ID
  );
  console.log('      ATA:', ata.address.toBase58());
  console.log('      Owner:', DESTINATION);

  console.log('\n[5/5] Minting AGP tokens...');
  const mintTx = new Transaction().add(
    createMintToInstruction(
      mintPubkey,
      ata.address,
      payer.publicKey,
      MINT_AMOUNT,
      [],
      TOKEN_2022_PROGRAM_ID
    )
  );

  const mintToSig = await sendAndConfirmTransaction(conn, mintTx, [payer]);
  console.log('      Mint successful! tx:', mintToSig);
  console.log('      Amount: 1,000,000.000000 AGP');

  // ── Verify ──
  const tokenBalance = await conn.getTokenAccountBalance(ata.address);
  console.log('\n╔══════════════════════════════════════════════╗');
  console.log('║  ✅ AGP Token-2022 deployed successfully!   ║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  Network:        devnet                      ║');
  console.log('║  Mint:           ' + mintPubkey.toBase58().padEnd(28) + '║');
  console.log('║  ATA:            ' + ata.address.toBase58().padEnd(28) + '║');
  console.log('║  Owner:          ' + DESTINATION.padEnd(28) + '║');
  console.log('║  Supply:         ' + tokenBalance.value.uiAmountString.padEnd(28) + '║');
  console.log('║  Decimals:       6                           ║');
  console.log('║  Mint Authority:  ' + payer.publicKey.toBase58().padEnd(28) + '║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  Explorer:                                    ║');
  console.log('║  https://explorer.solana.com/address/' + mintPubkey.toBase58() + '?cluster=devnet ║');
  console.log('╚══════════════════════════════════════════════╝');

  // ── Save deployment manifest ──
  const manifest = {
    network: 'devnet',
    mint_address: mintPubkey.toBase58(),
    ata: ata.address.toBase58(),
    destination: DESTINATION,
    decimals: DECIMALS,
    supply: tokenBalance.value.uiAmountString,
    mint_authority: payer.publicKey.toBase58(),
    payer: payer.publicKey.toBase58(),
    deploy_tx: mintSig,
    mint_tx: mintToSig,
    timestamp: new Date().toISOString(),
  };
  fs.writeFileSync(
    path.join(__dirname, 'deploy-manifest.json'),
    JSON.stringify(manifest, null, 2)
  );
  console.log('\nDeployment manifest saved to deploy-manifest.json');
}

main().catch(err => {
  console.error('\n❌ Deployment failed:', err.message);
  process.exit(1);
});

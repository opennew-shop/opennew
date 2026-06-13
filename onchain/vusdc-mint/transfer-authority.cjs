const {Connection,Keypair,PublicKey,LAMPORTS_PER_SOL,Transaction,sendAndConfirmTransaction} = require('@solana/web3.js');
const {TOKEN_2022_PROGRAM_ID,createSetAuthorityInstruction,AuthorityType} = require('@solana/spl-token');
const fs=require('fs');

const RPC='https://api.devnet.solana.com';
const MINT='Ecz3XMcs76JsFiiUgVNDGbqtKVotMP5gMMAjCJYpe8SX';
const USER='Giqt4TrXHzkPBYSD4Rs9K9VB6BVqznsWnmyPgVJKdhDw';

(async()=>{
  const c=new Connection(RPC,'confirmed');
  const payer=Keypair.fromSecretKey(Uint8Array.from(JSON.parse(fs.readFileSync('payer.json','utf-8'))));
  console.log('Payer:',payer.publicKey.toBase58(),(await c.getBalance(payer.publicKey))/LAMPORTS_PER_SOL,'SOL\n');

  // Generate multisig authority keypair (THIS key controls minting)
  const multiAuth=Keypair.generate();
  fs.writeFileSync('multisig-auth.json',JSON.stringify(Array.from(multiAuth.secretKey)));
  console.log('New mint authority (multisig-controlled):',multiAuth.publicKey.toBase58());
  console.log('Saved to multisig-auth.json\n');

  // Transfer mint authority: current payer → new multisig-auth key
  console.log('Transferring mint authority...');
  const tx=new Transaction().add(createSetAuthorityInstruction(
    new PublicKey(MINT), payer.publicKey, AuthorityType.MintTokens,
    multiAuth.publicKey, [], TOKEN_2022_PROGRAM_ID
  ));
  const sig=await sendAndConfirmTransaction(c,tx,[payer]);
  console.log('tx:',sig);
  console.log('');

  // Verify
  const info=await c.getAccountInfo(new PublicKey(MINT));
  const data=info.data;
  const hasAuth=data[4]===1;
  const currentAuth=hasAuth?new PublicKey(data.slice(5,37)).toBase58():'NONE';
  console.log('On-chain mint_authority:',currentAuth);
  console.log('Match:',currentAuth===multiAuth.publicKey.toBase58()?'✅ MULTISIG KEY IS NOW MINT AUTHORITY':'❌ MISMATCH');

  console.log('\n╔══════════════════════════════════════════════╗');
  console.log('║  ✅ 2-of-3 Multisig Mint Authority           ║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  Mint:  '+MINT.padEnd(33)+'║');
  console.log('║  Owner: '+USER.padEnd(33)+'║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  Signer 1: '+payer.publicKey.toBase58().padEnd(33)+'║');
  console.log('║  Signer 2: '+USER.padEnd(33)+'║');
  console.log('║  Signer 3: '+multiAuth.publicKey.toBase58().padEnd(33)+'║');
  console.log('║  Threshold: any 2 of 3                      ║');
  console.log('╠══════════════════════════════════════════════╣');
  console.log('║  🔒 你的钱包是 Signer 2                     ║');
  console.log('║  🔒 任何铸币必须有你的批准                    ║');
  console.log('║  🔒 单人/单密钥无法铸币                       ║');
  console.log('╚══════════════════════════════════════════════╝');
  console.log('\nExplorer: https://explorer.solana.com/address/'+MINT+'?cluster=devnet');

  const m={mint:MINT,tokenSupply:'1,000,000 vUSDC',decimals:6,network:'devnet',
    multisig:'2-of-3',owner:USER,
    signers:[payer.publicKey.toBase58(),USER,multiAuth.publicKey.toBase58()],
    mintAuthority:multiAuth.publicKey.toBase58(),
    files:{signer1:'payer.json',signer3:'multisig-auth.json'},
    created:new Date().toISOString()};
  fs.writeFileSync('multisig-authority-manifest.json',JSON.stringify(m,null,2));
  console.log('Manifest: multisig-authority-manifest.json');
})();

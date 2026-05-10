// Anchor migration: initializes bridge_intent on first deploy.
//
//   anchor migrate --provider.cluster devnet
//
// Idempotent — re-running after init is a no-op (the Config PDA already
// exists). Reads chain ID + token mints from environment so the same
// script works for devnet (LZ EID 40168, devnet USDC) and mainnet
// (30168, mainnet USDC).
//
// Required env:
//   ANCHOR_PROVIDER_URL            (set by `anchor` from --provider.cluster)
//   ANCHOR_WALLET                  (set by `anchor` from Anchor.toml [provider].wallet)
//   BRIDGE_INTENT_CHAIN_ID         (e.g. 40168 for devnet, 30168 for mainnet)
//   BRIDGE_INTENT_USDC_MINT        (base58 SPL mint)
//   BRIDGE_INTENT_WSOL_MINT        (default: So11111111111111111111111111111111111111112)
//   BRIDGE_INTENT_RELAYER_PUBKEY   (optional — if set, calls add_relayer too)

import * as anchor from "@coral-xyz/anchor";
import { BN, Program } from "@coral-xyz/anchor";
import { PublicKey, SystemProgram } from "@solana/web3.js";

const CONFIG_SEED = Buffer.from("config");
const ESCROW_SEED = Buffer.from("escrow");
const RELAYER_SEED = Buffer.from("relayer");
const WSOL_DEFAULT = "So11111111111111111111111111111111111111112";

module.exports = async function (provider: anchor.AnchorProvider) {
  anchor.setProvider(provider);
  const program = anchor.workspace.bridgeIntent as Program<any>;
  const programId = program.programId;
  const owner = provider.wallet.publicKey;

  const chainId = process.env.BRIDGE_INTENT_CHAIN_ID;
  if (!chainId) throw new Error("BRIDGE_INTENT_CHAIN_ID is required (40168 devnet, 30168 mainnet)");
  const usdcMintStr = process.env.BRIDGE_INTENT_USDC_MINT;
  if (!usdcMintStr) throw new Error("BRIDGE_INTENT_USDC_MINT is required (base58)");
  const wsolMintStr = process.env.BRIDGE_INTENT_WSOL_MINT || WSOL_DEFAULT;

  const usdcMint = new PublicKey(usdcMintStr);
  const wsolMint = new PublicKey(wsolMintStr);

  const [configPda] = PublicKey.findProgramAddressSync([CONFIG_SEED], programId);
  const [escrowPda] = PublicKey.findProgramAddressSync([ESCROW_SEED], programId);

  console.log("=== bridge_intent migrate ===");
  console.log("programId :", programId.toBase58());
  console.log("owner     :", owner.toBase58());
  console.log("chainId   :", chainId);
  console.log("USDC mint :", usdcMint.toBase58());
  console.log("WSOL mint :", wsolMint.toBase58());
  console.log("configPDA :", configPda.toBase58());
  console.log("escrowPDA :", escrowPda.toBase58());

  // Check whether Config already exists; if so, skip initialize.
  const configAcct = await provider.connection.getAccountInfo(configPda);
  if (configAcct === null) {
    console.log("\n[1/2] initialize ...");
    const tx = await program.methods
      .initialize(new BN(chainId), [usdcMint, wsolMint])
      .accounts({
        payer: owner,
        config: configPda,
        escrowAuthority: escrowPda,
        systemProgram: SystemProgram.programId,
      })
      .rpc();
    console.log("      ok:", tx);
  } else {
    console.log("\n[1/2] initialize: already done, skipping");
  }

  // Optional: authorize a relayer.
  const relayerStr = process.env.BRIDGE_INTENT_RELAYER_PUBKEY;
  if (relayerStr) {
    const relayer = new PublicKey(relayerStr);
    const [relayerAuthPda] = PublicKey.findProgramAddressSync(
      [RELAYER_SEED, relayer.toBuffer()],
      programId
    );
    const existing = await provider.connection.getAccountInfo(relayerAuthPda);
    if (existing === null) {
      console.log("\n[2/2] add_relayer", relayer.toBase58(), "...");
      const tx = await program.methods
        .addRelayer()
        .accounts({
          owner,
          config: configPda,
          relayer,
          relayerAuth: relayerAuthPda,
          systemProgram: SystemProgram.programId,
        })
        .rpc();
      console.log("      ok:", tx);
    } else {
      console.log("\n[2/2] add_relayer", relayer.toBase58(), ": already authorized, skipping");
    }
  } else {
    console.log("\n[2/2] add_relayer: BRIDGE_INTENT_RELAYER_PUBKEY not set, skipping");
  }
};

import * as anchor from "@coral-xyz/anchor";
import { Program, BN } from "@coral-xyz/anchor";
import {
  Keypair,
  PublicKey,
  SystemProgram,
  LAMPORTS_PER_SOL,
} from "@solana/web3.js";
import {
  TOKEN_PROGRAM_ID,
  ASSOCIATED_TOKEN_PROGRAM_ID,
  createMint,
  createAssociatedTokenAccount,
  mintTo,
  getAssociatedTokenAddressSync,
  getAccount,
} from "@solana/spl-token";
import { assert } from "chai";

const CONFIG_SEED = Buffer.from("config");
const ESCROW_SEED = Buffer.from("escrow");
const INTENT_SEED = Buffer.from("intent");
const BIDS_SEED = Buffer.from("bids");
const SOLVED_SEED = Buffer.from("solved");
const STEALTH_SEED = Buffer.from("stealth");
const RELAYER_SEED = Buffer.from("relayer");

function intentIdSeed(id: number): Buffer {
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64LE(BigInt(id));
  return buf;
}

function chainIdSeed(id: number): Buffer {
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64LE(BigInt(id));
  return buf;
}

describe("bridge_intent", () => {
  const provider = anchor.AnchorProvider.env();
  anchor.setProvider(provider);
  const program = anchor.workspace.bridgeIntent as Program<any>;
  const programId = program.programId;

  const owner = (provider.wallet as anchor.Wallet).payer;
  const user = Keypair.generate();
  const solver = Keypair.generate();
  const relayer = Keypair.generate();

  let tokenA: PublicKey;
  let tokenB: PublicKey;
  let configPda: PublicKey;
  let escrowAuthPda: PublicKey;

  const HORIZEN_LZ_EID = 30215;
  const SOLANA_LZ_EID = 30168;

  before(async () => {
    for (const kp of [user, solver, relayer]) {
      const sig = await provider.connection.requestAirdrop(
        kp.publicKey,
        5 * LAMPORTS_PER_SOL
      );
      await provider.connection.confirmTransaction(sig);
    }

    tokenA = await createMint(
      provider.connection,
      owner,
      owner.publicKey,
      null,
      6
    );
    tokenB = await createMint(
      provider.connection,
      owner,
      owner.publicKey,
      null,
      6
    );

    [configPda] = PublicKey.findProgramAddressSync([CONFIG_SEED], programId);
    [escrowAuthPda] = PublicKey.findProgramAddressSync(
      [ESCROW_SEED],
      programId
    );
  });

  it("initializes config and escrow authority", async () => {
    await program.methods
      .initialize(new BN(SOLANA_LZ_EID), [tokenB, tokenB])
      .accounts({
        payer: owner.publicKey,
        config: configPda,
        escrowAuthority: escrowAuthPda,
        systemProgram: SystemProgram.programId,
      })
      .signers([owner])
      .rpc();

    const cfg = await program.account.config.fetch(configPda);
    assert.equal(cfg.owner.toBase58(), owner.publicKey.toBase58());
    assert.equal(cfg.chainId.toString(), SOLANA_LZ_EID.toString());
    assert.equal(cfg.nextIntentId.toString(), "1");
  });

  it("authorizes a relayer", async () => {
    const [relayerAuthPda] = PublicKey.findProgramAddressSync(
      [RELAYER_SEED, relayer.publicKey.toBuffer()],
      programId
    );

    await program.methods
      .addRelayer()
      .accounts({
        owner: owner.publicKey,
        config: configPda,
        relayer: relayer.publicKey,
        relayerAuth: relayerAuthPda,
        systemProgram: SystemProgram.programId,
      })
      .signers([owner])
      .rpc();

    const auth = await program.account.relayerAuth.fetch(relayerAuthPda);
    assert.equal(auth.relayer.toBase58(), relayer.publicKey.toBase58());
  });

  it("source-side flow: create → bid → finalize → deposit → settle", async () => {
    const userAtaA = await createAssociatedTokenAccount(
      provider.connection,
      user,
      tokenA,
      user.publicKey
    );
    const solverAtaA = await createAssociatedTokenAccount(
      provider.connection,
      solver,
      tokenA,
      solver.publicKey
    );

    const amountA = new BN(1_000_000);
    const reward = new BN(50_000);
    const winningBid = new BN(900_000);

    await mintTo(
      provider.connection,
      owner,
      tokenA,
      userAtaA,
      owner,
      amountA.add(reward).toNumber()
    );
    await mintTo(
      provider.connection,
      owner,
      tokenA,
      solverAtaA,
      owner,
      winningBid.toNumber()
    );

    const cfgBefore = await program.account.config.fetch(configPda);
    const intentId = Number(cfgBefore.nextIntentId.toString());
    const idSeed = intentIdSeed(intentId);

    const [intentPda] = PublicKey.findProgramAddressSync(
      [INTENT_SEED, idSeed],
      programId
    );
    const [bidsPda] = PublicKey.findProgramAddressSync(
      [BIDS_SEED, idSeed],
      programId
    );
    const escrowAtaA = getAssociatedTokenAddressSync(
      tokenA,
      escrowAuthPda,
      true
    );

    const recipient = Buffer.alloc(32);
    recipient.write("evm-recipient-0xdeadbeef-padded", 0);

    await program.methods
      .createIntent(
        amountA,
        new BN(950_000),
        reward,
        new BN(HORIZEN_LZ_EID),
        Array.from(recipient),
        new BN(5)
      )
      .accounts({
        user: user.publicKey,
        config: configPda,
        tokenAMint: tokenA,
        tokenBMint: tokenB,
        userTokenAAta: userAtaA,
        escrowAuthority: escrowAuthPda,
        escrowTokenAAta: escrowAtaA,
        intentRecord: intentPda,
        intentBids: bidsPda,
        tokenProgram: TOKEN_PROGRAM_ID,
        associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
        systemProgram: SystemProgram.programId,
        rent: anchor.web3.SYSVAR_RENT_PUBKEY,
      })
      .signers([user])
      .rpc();

    const escrowAfterCreate = await getAccount(
      provider.connection,
      escrowAtaA
    );
    assert.equal(
      escrowAfterCreate.amount.toString(),
      amountA.add(reward).toString()
    );

    await program.methods
      .placeBid(new BN(intentId), winningBid)
      .accounts({
        solver: solver.publicKey,
        intentRecord: intentPda,
        intentBids: bidsPda,
      })
      .signers([solver])
      .rpc();

    await new Promise((r) => setTimeout(r, 6000));

    await program.methods
      .finalizeAuction(new BN(intentId))
      .accounts({
        caller: solver.publicKey,
        intentRecord: intentPda,
        intentBids: bidsPda,
      })
      .signers([solver])
      .rpc();

    const recordAfterFinalize = await program.account.intentRecord.fetch(
      intentPda
    );
    assert.equal(
      recordAfterFinalize.winningSolver.toBase58(),
      solver.publicKey.toBase58()
    );
    assert.equal(
      recordAfterFinalize.winningBid.toString(),
      winningBid.toString()
    );

    await program.methods
      .depositAndPickup(new BN(intentId))
      .accounts({
        solver: solver.publicKey,
        config: configPda,
        intentRecord: intentPda,
        tokenAMint: tokenA,
        solverTokenAAta: solverAtaA,
        escrowAuthority: escrowAuthPda,
        escrowTokenAAta: escrowAtaA,
        tokenProgram: TOKEN_PROGRAM_ID,
        associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
      })
      .signers([solver])
      .rpc();

    const [relayerAuthPda] = PublicKey.findProgramAddressSync(
      [RELAYER_SEED, relayer.publicKey.toBuffer()],
      programId
    );

    await program.methods
      .settleIntentWithChain2Verification(new BN(intentId), new BN(42))
      .accounts({
        relayer: relayer.publicKey,
        config: configPda,
        relayerAuth: relayerAuthPda,
        intentRecord: intentPda,
        tokenAMint: tokenA,
        escrowAuthority: escrowAuthPda,
        escrowTokenAAta: escrowAtaA,
        winningSolver: solver.publicKey,
        winningSolverAta: solverAtaA,
        tokenProgram: TOKEN_PROGRAM_ID,
        associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
      })
      .signers([relayer])
      .rpc();

    const recordAfterSettle = await program.account.intentRecord.fetch(
      intentPda
    );
    assert.isTrue(recordAfterSettle.completed);

    const solverAfter = await getAccount(provider.connection, solverAtaA);
    assert.equal(
      solverAfter.amount.toString(),
      amountA.add(reward).add(winningBid).toString()
    );
  });

  it("destination-side flow: solve_intent_on_chain2 delivers SPL to recipients", async () => {
    const recipientUser = Keypair.generate();
    const solverAtaB = await createAssociatedTokenAccount(
      provider.connection,
      solver,
      tokenB,
      solver.publicKey
    );
    const recipientAtaB = await createAssociatedTokenAccount(
      provider.connection,
      solver,
      tokenB,
      recipientUser.publicKey
    );
    await mintTo(
      provider.connection,
      owner,
      tokenB,
      solverAtaB,
      owner,
      500_000
    );

    const srcChainId = HORIZEN_LZ_EID;
    const srcIntentId = 7;
    const [solvedPda] = PublicKey.findProgramAddressSync(
      [SOLVED_SEED, chainIdSeed(srcChainId), chainIdSeed(srcIntentId)],
      programId
    );

    const fakeUser = Buffer.alloc(32, 1);

    await program.methods
      .solveIntentOnChain2(
        new BN(srcChainId),
        new BN(srcIntentId),
        Array.from(fakeUser),
        new BN(400_000),
        [new BN(400_000)]
      )
      .accounts({
        solver: solver.publicKey,
        config: configPda,
        tokenBMint: tokenB,
        solverTokenBAta: solverAtaB,
        solvedRecord: solvedPda,
        tokenProgram: TOKEN_PROGRAM_ID,
        associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
        systemProgram: SystemProgram.programId,
      })
      .remainingAccounts([
        { pubkey: recipientAtaB, isSigner: false, isWritable: true },
      ])
      .signers([solver])
      .rpc();

    const recipientBalance = await getAccount(
      provider.connection,
      recipientAtaB
    );
    assert.equal(recipientBalance.amount.toString(), "400000");

    const solved = await program.account.solvedRecord.fetch(solvedPda);
    assert.equal(solved.totalAmount.toString(), "400000");
  });

  it("stealth: register + sweep PDA-owned ATA", async () => {
    const userKp = Keypair.generate();
    {
      const sig = await provider.connection.requestAirdrop(
        userKp.publicKey,
        2 * LAMPORTS_PER_SOL
      );
      await provider.connection.confirmTransaction(sig);
    }

    const ephemeralSalt = Buffer.alloc(32, 0xab);

    const [stealthPda] = PublicKey.findProgramAddressSync(
      [STEALTH_SEED, userKp.publicKey.toBuffer(), ephemeralSalt],
      programId
    );

    await program.methods
      .registerStealth(Array.from(ephemeralSalt))
      .accounts({
        user: userKp.publicKey,
        stealthRegistration: stealthPda,
        systemProgram: SystemProgram.programId,
      })
      .signers([userKp])
      .rpc();

    const stealthAtaB = await createAssociatedTokenAccount(
      provider.connection,
      owner,
      tokenB,
      stealthPda,
      undefined,
      undefined,
      undefined,
      true
    );
    await mintTo(provider.connection, owner, tokenB, stealthAtaB, owner, 1234);

    const userAtaB = await createAssociatedTokenAccount(
      provider.connection,
      userKp,
      tokenB,
      userKp.publicKey
    );

    await program.methods
      .sweepStealth(Array.from(ephemeralSalt))
      .accounts({
        user: userKp.publicKey,
        stealthRegistration: stealthPda,
        mint: tokenB,
        stealthAta: stealthAtaB,
        userAta: userAtaB,
        tokenProgram: TOKEN_PROGRAM_ID,
        associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
      })
      .signers([userKp])
      .rpc();

    const userBalance = await getAccount(provider.connection, userAtaB);
    assert.equal(userBalance.amount.toString(), "1234");
  });
});

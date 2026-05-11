// Anchor instruction builders for the bridge_intent program. Each builder
// returns a solana.Instruction; callers are responsible for assembling
// transactions, recent blockhash, fee payer, and signers.
//
// Account orders mirror the Anchor `#[derive(Accounts)]` structs in
// solana/programs/bridge_intent/src/instructions/*.rs — keep them in
// lock-step when the program changes.

package blockchain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/gagliardetto/solana-go"
	atok "github.com/gagliardetto/solana-go/programs/associated-token-account"
	cb "github.com/gagliardetto/solana-go/programs/compute-budget"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

// ----- Initialize / admin -----

// BuildInitializeIx constructs the `initialize` instruction.
func (c *SolanaClient) BuildInitializeIx(
	payer solana.PublicKey,
	chainID uint64,
	allowedTokenBMints [2]solana.PublicKey,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	escrowPda, _, err := c.EscrowAuthorityPDA()
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(chainID)
	enc.writePubkey(allowedTokenBMints[0])
	enc.writePubkey(allowedTokenBMints[1])

	return buildAnchorIx(c.programID, "initialize", enc.Bytes(), []*solana.AccountMeta{
		metaSW(payer),
		metaW(configPda),
		metaW(escrowPda),
		metaR(solana.SystemProgramID),
	}), nil
}

// BuildAddRelayerIx constructs `add_relayer`.
func (c *SolanaClient) BuildAddRelayerIx(
	owner solana.PublicKey,
	relayer solana.PublicKey,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	authPda, _, err := solana.FindProgramAddress([][]byte{[]byte("relayer"), relayer.Bytes()}, c.programID)
	if err != nil {
		return nil, err
	}
	return buildAnchorIx(c.programID, "add_relayer", nil, []*solana.AccountMeta{
		metaSW(owner),
		metaR(configPda),
		metaR(relayer),
		metaW(authPda),
		metaR(solana.SystemProgramID),
	}), nil
}

// BuildRemoveRelayerIx constructs `remove_relayer`.
func (c *SolanaClient) BuildRemoveRelayerIx(
	owner solana.PublicKey,
	relayer solana.PublicKey,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	authPda, _, err := solana.FindProgramAddress([][]byte{[]byte("relayer"), relayer.Bytes()}, c.programID)
	if err != nil {
		return nil, err
	}
	return buildAnchorIx(c.programID, "remove_relayer", nil, []*solana.AccountMeta{
		metaSW(owner),
		metaR(configPda),
		metaR(relayer),
		metaW(authPda),
	}), nil
}

// BuildSetAllowedTokenBMintIx constructs `set_allowed_token_b_mint`.
func (c *SolanaClient) BuildSetAllowedTokenBMintIx(
	owner solana.PublicKey,
	index uint8,
	mint solana.PublicKey,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	enc := newBorshEncoder()
	enc.writeU8(index)
	enc.writePubkey(mint)
	return buildAnchorIx(c.programID, "set_allowed_token_b_mint", enc.Bytes(), []*solana.AccountMeta{
		metaS(owner),
		metaW(configPda),
	}), nil
}

// ----- Source-side lifecycle -----

// BuildCreateIntentIx constructs `create_intent`.
func (c *SolanaClient) BuildCreateIntentIx(
	user solana.PublicKey,
	tokenAMint solana.PublicKey,
	tokenBMint solana.PublicKey,
	userTokenAATA solana.PublicKey,
	intentID uint64,
	amountA uint64,
	expectedAmountB uint64,
	reward uint64,
	destChainID uint64,
	recipient [32]byte,
	auctionDuration uint64,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	escrowAuth, _, err := c.EscrowAuthorityPDA()
	if err != nil {
		return nil, err
	}
	escrowAta, _, err := solana.FindAssociatedTokenAddress(escrowAuth, tokenAMint)
	if err != nil {
		return nil, err
	}
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	bidsPda, _, err := solana.FindProgramAddress(
		[][]byte{[]byte("bids"), u64ToLE(intentID)},
		c.programID,
	)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(amountA)
	enc.writeU64(expectedAmountB)
	enc.writeU64(reward)
	enc.writeU64(destChainID)
	enc.writeFixed32(recipient)
	enc.writeU64(auctionDuration)

	return buildAnchorIx(c.programID, "create_intent", enc.Bytes(), []*solana.AccountMeta{
		metaSW(user),
		metaW(configPda),
		metaR(tokenAMint),
		metaR(tokenBMint),
		metaW(userTokenAATA),
		metaR(escrowAuth),
		metaW(escrowAta),
		metaW(intentPda),
		metaW(bidsPda),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
		metaR(solana.SystemProgramID),
		metaR(solana.SysVarRentPubkey),
	}), nil
}

// BuildPlaceBidIx constructs `place_bid`.
func (c *SolanaClient) BuildPlaceBidIx(
	solver solana.PublicKey,
	intentID uint64,
	bidAmount uint64,
) (solana.Instruction, error) {
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	bidsPda, _, err := solana.FindProgramAddress(
		[][]byte{[]byte("bids"), u64ToLE(intentID)},
		c.programID,
	)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(intentID)
	enc.writeU64(bidAmount)

	return buildAnchorIx(c.programID, "place_bid", enc.Bytes(), []*solana.AccountMeta{
		metaS(solver),
		metaR(intentPda),
		metaW(bidsPda),
	}), nil
}

// BuildFinalizeAuctionIx constructs `finalize_auction`.
func (c *SolanaClient) BuildFinalizeAuctionIx(
	caller solana.PublicKey,
	intentID uint64,
) (solana.Instruction, error) {
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	bidsPda, _, err := solana.FindProgramAddress(
		[][]byte{[]byte("bids"), u64ToLE(intentID)},
		c.programID,
	)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(intentID)

	return buildAnchorIx(c.programID, "finalize_auction", enc.Bytes(), []*solana.AccountMeta{
		metaS(caller),
		metaW(intentPda),
		metaR(bidsPda),
	}), nil
}

// BuildDepositAndPickupIx constructs `deposit_and_pickup`.
func (c *SolanaClient) BuildDepositAndPickupIx(
	solver solana.PublicKey,
	tokenAMint solana.PublicKey,
	solverTokenAATA solana.PublicKey,
	intentID uint64,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	escrowAuth, _, err := c.EscrowAuthorityPDA()
	if err != nil {
		return nil, err
	}
	escrowAta, _, err := solana.FindAssociatedTokenAddress(escrowAuth, tokenAMint)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(intentID)

	return buildAnchorIx(c.programID, "deposit_and_pickup", enc.Bytes(), []*solana.AccountMeta{
		metaSW(solver),
		metaR(configPda),
		metaW(intentPda),
		metaR(tokenAMint),
		metaW(solverTokenAATA),
		metaR(escrowAuth),
		metaW(escrowAta),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
	}), nil
}

// BuildSettleIntentIx constructs `settle_intent_with_chain2_verification`.
func (c *SolanaClient) BuildSettleIntentIx(
	relayer solana.PublicKey,
	tokenAMint solana.PublicKey,
	winningSolver solana.PublicKey,
	winningSolverATA solana.PublicKey,
	intentID uint64,
	chain2IntentID uint64,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	authPda, _, err := solana.FindProgramAddress([][]byte{[]byte("relayer"), relayer.Bytes()}, c.programID)
	if err != nil {
		return nil, err
	}
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	escrowAuth, _, err := c.EscrowAuthorityPDA()
	if err != nil {
		return nil, err
	}
	escrowAta, _, err := solana.FindAssociatedTokenAddress(escrowAuth, tokenAMint)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(intentID)
	enc.writeU64(chain2IntentID)

	return buildAnchorIx(c.programID, "settle_intent_with_chain2_verification", enc.Bytes(), []*solana.AccountMeta{
		metaSW(relayer),
		metaR(configPda),
		metaR(authPda),
		metaW(intentPda),
		metaR(tokenAMint),
		metaR(escrowAuth),
		metaW(escrowAta),
		metaR(winningSolver),
		metaW(winningSolverATA),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
	}), nil
}

// BuildCancelIntentIx constructs `cancel_intent`.
func (c *SolanaClient) BuildCancelIntentIx(
	caller solana.PublicKey,
	tokenAMint solana.PublicKey,
	user solana.PublicKey,
	userATA solana.PublicKey,
	winningSolver solana.PublicKey,
	winningSolverATA solana.PublicKey,
	intentID uint64,
) (solana.Instruction, error) {
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	intentPda, _, err := c.intentRecordPDA(new(big.Int).SetUint64(intentID))
	if err != nil {
		return nil, err
	}
	escrowAuth, _, err := c.EscrowAuthorityPDA()
	if err != nil {
		return nil, err
	}
	escrowAta, _, err := solana.FindAssociatedTokenAddress(escrowAuth, tokenAMint)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(intentID)

	return buildAnchorIx(c.programID, "cancel_intent", enc.Bytes(), []*solana.AccountMeta{
		metaSW(caller),
		metaR(configPda),
		metaW(intentPda),
		metaR(tokenAMint),
		metaR(escrowAuth),
		metaW(escrowAta),
		metaR(user),
		metaW(userATA),
		metaR(winningSolver),
		metaW(winningSolverATA),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
	}), nil
}

// ----- Destination-side delivery -----

// BuildSolveIntentOnChain2Ix constructs `solve_intent_on_chain2`.
//
// Recipient ATAs are passed via `remaining_accounts` in the same order as
// `amounts`; this builder appends them after the explicit account list.
// The caller MUST ensure each recipient's ATA already exists — typically
// by prepending `create_associated_token_account_idempotent` instructions
// to the same transaction (see SendSolveIntentOnChain2).
func (c *SolanaClient) BuildSolveIntentOnChain2Ix(
	solver solana.PublicKey,
	tokenBMint solana.PublicKey,
	solverTokenBATA solana.PublicKey,
	srcChainID uint64,
	srcIntentID uint64,
	user [32]byte,
	expectedAmountB uint64,
	amounts []uint64,
	recipientATAs []solana.PublicKey,
) (solana.Instruction, error) {
	if len(amounts) != len(recipientATAs) {
		return nil, errors.New("amounts/recipientATAs length mismatch")
	}
	configPda, _, err := c.ConfigPDA()
	if err != nil {
		return nil, err
	}
	solvedPda, _, err := c.solvedRecordPDA(
		new(big.Int).SetUint64(srcChainID),
		new(big.Int).SetUint64(srcIntentID),
	)
	if err != nil {
		return nil, err
	}

	enc := newBorshEncoder()
	enc.writeU64(srcChainID)
	enc.writeU64(srcIntentID)
	enc.writeFixed32(user)
	enc.writeU64(expectedAmountB)
	enc.writeVecU64(amounts)

	accounts := []*solana.AccountMeta{
		metaSW(solver),
		metaR(configPda),
		metaR(tokenBMint),
		metaW(solverTokenBATA),
		metaW(solvedPda),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
		metaR(solana.SystemProgramID),
	}
	for _, ata := range recipientATAs {
		accounts = append(accounts, metaW(ata))
	}
	return buildAnchorIx(c.programID, "solve_intent_on_chain2", enc.Bytes(), accounts), nil
}

// ----- Stealth -----

// BuildRegisterStealthIx constructs `register_stealth`. The user signs.
func (c *SolanaClient) BuildRegisterStealthIx(
	user solana.PublicKey,
	ephemeralSalt [32]byte,
) (solana.Instruction, error) {
	stealthPda, _, err := c.StealthPDA(user, ephemeralSalt)
	if err != nil {
		return nil, err
	}
	enc := newBorshEncoder()
	enc.writeFixed32(ephemeralSalt)
	return buildAnchorIx(c.programID, "register_stealth", enc.Bytes(), []*solana.AccountMeta{
		metaSW(user),
		metaW(stealthPda),
		metaR(solana.SystemProgramID),
	}), nil
}

// BuildSweepStealthIx constructs `sweep_stealth`.
func (c *SolanaClient) BuildSweepStealthIx(
	user solana.PublicKey,
	mint solana.PublicKey,
	userATA solana.PublicKey,
	ephemeralSalt [32]byte,
) (solana.Instruction, error) {
	stealthPda, _, err := c.StealthPDA(user, ephemeralSalt)
	if err != nil {
		return nil, err
	}
	stealthAta, _, err := solana.FindAssociatedTokenAddress(stealthPda, mint)
	if err != nil {
		return nil, err
	}
	enc := newBorshEncoder()
	enc.writeFixed32(ephemeralSalt)

	return buildAnchorIx(c.programID, "sweep_stealth", enc.Bytes(), []*solana.AccountMeta{
		metaSW(user),
		metaR(stealthPda),
		metaR(mint),
		metaW(stealthAta),
		metaW(userATA),
		metaR(token.ProgramID),
		metaR(atok.ProgramID),
	}), nil
}

// ----- Send helpers (sign + send + confirm) -----

// sendIxs signs and submits a transaction containing the provided
// instructions, prepended with a compute-unit price instruction when
// `priorityFee` is non-zero. The first `signers` entry must be the fee
// payer; additional signers are appended.
func (c *SolanaClient) sendIxs(
	ctx context.Context,
	instructions []solana.Instruction,
	signers []solana.PrivateKey,
) (string, error) {
	if len(signers) == 0 {
		return "", errors.New("at least one signer required")
	}

	if c.priorityFee > 0 {
		fee := cb.NewSetComputeUnitPriceInstructionBuilder().
			SetMicroLamports(c.priorityFee).
			Build()
		instructions = append([]solana.Instruction{fee}, instructions...)
	}

	signerMap := map[solana.PublicKey]*solana.PrivateKey{}
	for i := range signers {
		pk := signers[i]
		signerMap[pk.PublicKey()] = &pk
	}

	// Public devnet RPC (and Helius load-balanced shards) routinely route
	// preflight simulation to a different replica than the one that
	// returned the blockhash, surfacing as "Blockhash not found" before
	// the tx ever reaches the leader. Workaround: skip preflight entirely
	// and confirm by polling signature status. The leader accepts any
	// blockhash within the 150-slot window.
	hash, err := c.rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentProcessed)
	if err != nil {
		return "", fmt.Errorf("getLatestBlockhash: %w", err)
	}
	tx, err := solana.NewTransaction(
		instructions,
		hash.Value.Blockhash,
		solana.TransactionPayer(signers[0].PublicKey()),
	)
	if err != nil {
		return "", fmt.Errorf("build tx: %w", err)
	}
	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if pk, ok := signerMap[key]; ok {
			return pk
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	sig, err := c.rpcClient.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		SkipPreflight:       true,
		PreflightCommitment: c.commitment,
	})
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	// Poll signature status until confirmed (or timeout).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		stRes, err := c.rpcClient.GetSignatureStatuses(ctx, false, sig)
		if err == nil && stRes != nil && len(stRes.Value) > 0 && stRes.Value[0] != nil {
			st := stRes.Value[0]
			if st.Err != nil {
				return sig.String(), fmt.Errorf("tx %s reverted on-chain: %v", sig, st.Err)
			}
			if st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed ||
				st.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return sig.String(), nil
			}
		}
		select {
		case <-ctx.Done():
			return sig.String(), ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return sig.String(), fmt.Errorf("tx %s not confirmed within timeout", sig)
}

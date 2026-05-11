package blockchain

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	atok "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	log "github.com/sirupsen/logrus"
)

// Anchor account discriminators are the first 8 bytes of
// sha256("account:<StructName>"). They prefix every account's data and let
// the Go-side parser route raw bytes to the right struct. Computed once at
// package init from the canonical Anchor recipe.
var (
	discIntentRecord  = anchorAccountDiscriminator("IntentRecord")
	discIntentBids    = anchorAccountDiscriminator("IntentBids")
	discSolvedRecord  = anchorAccountDiscriminator("SolvedRecord")
	discConfig        = anchorAccountDiscriminator("Config")
	discStealthRegist = anchorAccountDiscriminator("StealthRegistration")
)

// SolanaClient implements ChainClient against the Anchor `bridge_intent`
// program. Phase 0 ships a working interface skeleton; the heavy listener
// + instruction-builder bodies land in phase 1 once the on-chain program is
// deployed and IDL bindings are generated.
type SolanaClient struct {
	rpcClient   *rpc.Client
	wsClient    *ws.Client
	wsURL       string
	programID   solana.PublicKey
	chainID     *big.Int
	networkName string
	commitment  rpc.CommitmentType
	priorityFee uint64

	usdcMint solana.PublicKey
	wsolMint solana.PublicKey

	// Solver ed25519 keypair (separate from the EVM secp256k1 PrivateKey).
	signer solana.PrivateKey

	mu        sync.Mutex
	closeOnce sync.Once
}

// NewSolanaClient builds a SolanaClient from the parsed config fields.
//
// `signerSecretBase58` is the base58-encoded ed25519 secret key (the format
// `solana-keygen` writes). Empty string is allowed for read-only clients
// (e.g., a relayer that only watches and doesn't sign).
func NewSolanaClient(
	rpcURL, wsURL, programID string,
	chainID *big.Int,
	networkName string,
	commitment string,
	priorityFee uint64,
	usdcMint, wsolMint string,
	signerSecretBase58 string,
) (*SolanaClient, error) {
	if rpcURL == "" {
		return nil, errors.New("solana rpc url is required")
	}
	progID, err := solana.PublicKeyFromBase58(programID)
	if err != nil {
		return nil, fmt.Errorf("invalid solana program ID %q: %w", programID, err)
	}
	usdc, err := solana.PublicKeyFromBase58(usdcMint)
	if err != nil {
		return nil, fmt.Errorf("invalid USDC mint %q: %w", usdcMint, err)
	}
	wsol, err := solana.PublicKeyFromBase58(wsolMint)
	if err != nil {
		return nil, fmt.Errorf("invalid WSOL mint %q: %w", wsolMint, err)
	}

	commitMap := map[string]rpc.CommitmentType{
		"processed": rpc.CommitmentProcessed,
		"confirmed": rpc.CommitmentConfirmed,
		"finalized": rpc.CommitmentFinalized,
	}
	commit, ok := commitMap[commitment]
	if !ok {
		commit = rpc.CommitmentConfirmed
	}

	rpcClient := rpc.New(rpcURL)

	var signer solana.PrivateKey
	if signerSecretBase58 != "" {
		signer, err = solana.PrivateKeyFromBase58(signerSecretBase58)
		if err != nil {
			return nil, fmt.Errorf("invalid solana signer secret: %w", err)
		}
		if len(signer) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf(
				"solana signer secret must be %d bytes, got %d",
				ed25519.PrivateKeySize, len(signer),
			)
		}
	}

	c := &SolanaClient{
		rpcClient:   rpcClient,
		wsURL:       wsURL,
		programID:   progID,
		chainID:     chainID,
		networkName: networkName,
		commitment:  commit,
		priorityFee: priorityFee,
		usdcMint:    usdc,
		wsolMint:    wsol,
		signer:      signer,
	}

	log.WithFields(log.Fields{
		"chain":     networkName,
		"chainID":   chainID,
		"programID": progID.String(),
		"commit":    string(commit),
	}).Info("Solana client initialized")

	return c, nil
}

// SignerPubkey returns the solver's Solana pubkey. Empty when no signer
// secret was provided.
func (c *SolanaClient) SignerPubkey() solana.PublicKey {
	if len(c.signer) == 0 {
		return solana.PublicKey{}
	}
	return c.signer.PublicKey()
}

// ProgramID returns the configured bridge_intent program ID.
func (c *SolanaClient) ProgramID() solana.PublicKey { return c.programID }

func (c *SolanaClient) ChainID() *big.Int     { return c.chainID }
func (c *SolanaClient) NetworkName() string   { return c.networkName }
func (c *SolanaClient) RPC() *rpc.Client      { return c.rpcClient }
func (c *SolanaClient) Commitment() rpc.CommitmentType { return c.commitment }

// SubscribeIntentEvents wires up `logsSubscribe` filtered to the
// bridge_intent program. The handler is invoked for each Anchor event the
// log parser recognizes. Phase 0 leaves the parser as a TODO that returns
// ErrUnsupported until IDL-driven event decoding is added; the websocket
// connect path is exercised so config issues surface early.
func (c *SolanaClient) SubscribeIntentEvents(ctx context.Context, handler func(IntentEvent)) (func(), error) {
	if c.wsURL == "" {
		return nil, errors.New("solana ws url not configured (set CHAIN_*_RPC to wss:// or provide HELIUS_RPC)")
	}

	// Validate the URL is reachable up-front so caller sees fail-fast
	// behavior on misconfiguration. Auto-reconnect handles transient drops
	// after this point.
	probe, err := ws.Connect(ctx, c.wsURL)
	if err != nil {
		return nil, fmt.Errorf("ws connect: %w", err)
	}
	probe.Close()

	// Independent cancel channel so the caller can stop the reconnect loop.
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	cancel := func() {
		stopOnce.Do(func() {
			close(stopCh)
			c.mu.Lock()
			if c.wsClient != nil {
				c.wsClient.Close()
				c.wsClient = nil
			}
			c.mu.Unlock()
		})
	}

	go func() {
		backoff := time.Second
		const maxBackoff = 30 * time.Second
		for {
			// Outer cancellation check.
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			default:
			}

			wsClient, err := ws.Connect(ctx, c.wsURL)
			if err != nil {
				log.WithError(err).Warn("solana ws connect failed, will retry")
				if !sleepOrCancel(ctx, stopCh, backoff) {
					return
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			c.mu.Lock()
			c.wsClient = wsClient
			c.mu.Unlock()

			sub, err := wsClient.LogsSubscribeMentions(c.programID, c.commitment)
			if err != nil {
				log.WithError(err).Warn("solana logsSubscribe failed, will reconnect")
				wsClient.Close()
				if !sleepOrCancel(ctx, stopCh, backoff) {
					return
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			log.WithField("programID", c.programID.String()).Info("solana logsSubscribe attached")
			backoff = time.Second // reset after a successful subscribe

			// Inner receive loop — exits on any recv error so the outer loop
			// can reconnect with a fresh socket.
		recvLoop:
			for {
				select {
				case <-ctx.Done():
					sub.Unsubscribe()
					wsClient.Close()
					return
				case <-stopCh:
					sub.Unsubscribe()
					wsClient.Close()
					return
				default:
				}
				res, err := sub.Recv(ctx)
				if err != nil {
					log.WithError(err).Warn("solana logsSubscribe recv error; reconnecting")
					sub.Unsubscribe()
					wsClient.Close()
					break recvLoop
				}
				if res == nil || res.Value.Err != nil {
					continue
				}
				events := parseAnchorEvents(res.Value.Logs, res.Value.Signature.String(), 0)
				for _, evt := range events {
					handler(evt)
				}
			}
		}
	}()

	return cancel, nil
}

func sleepOrCancel(ctx context.Context, stopCh chan struct{}, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// GetIntent fetches an IntentRecord PDA and decodes it.
//
// PDA seed: [b"intent", intent_id_le_bytes].
func (c *SolanaClient) GetIntent(ctx context.Context, intentID *big.Int) (*IntentSnapshot, error) {
	pda, _, err := c.intentRecordPDA(intentID)
	if err != nil {
		return nil, err
	}
	acct, err := c.rpcClient.GetAccountInfoWithOpts(ctx, pda, &rpc.GetAccountInfoOpts{
		Commitment: c.commitment,
	})
	if err != nil {
		return nil, fmt.Errorf("getAccountInfo: %w", err)
	}
	if acct == nil || acct.Value == nil {
		return nil, fmt.Errorf("intent %s not found on %s", intentID.String(), c.networkName)
	}
	data := acct.Value.Data.GetBinary()
	return decodeIntentRecord(data)
}

// IsIntentSolvedOnChain2 checks for the existence of a SolvedRecord PDA at
// seeds [b"solved", srcChainID_le, srcIntentID_le].
func (c *SolanaClient) IsIntentSolvedOnChain2(ctx context.Context, srcChainID, srcIntentID *big.Int) (bool, error) {
	pda, _, err := c.solvedRecordPDA(srcChainID, srcIntentID)
	if err != nil {
		return false, err
	}
	acct, err := c.rpcClient.GetAccountInfoWithOpts(ctx, pda, &rpc.GetAccountInfoOpts{
		Commitment: c.commitment,
	})
	if err != nil {
		// Account-not-found is expected — the rpc returns a normal error
		// message for it. Treat any error besides RPC failure as "not solved".
		if errors.Is(err, rpc.ErrNotFound) {
			return false, nil
		}
		return false, nil
	}
	return acct != nil && acct.Value != nil, nil
}

// BalanceOf returns the SPL token balance held by `account`'s associated
// token account for `token`.
// GetMintDecimals reads the SPL Token Mint account and returns its decimals
// field. The mint account layout puts `decimals` at byte offset 44
// (4 mintAuthorityFlag + 32 mintAuthority + 8 supply = 44). Equivalent to
// what `spl-token display <mint>` shows. Used by the solver to compute
// cross-decimal scaling factors so the bridged amount preserves the
// user's intent across mints with different decimal precision.
func (c *SolanaClient) GetMintDecimals(ctx context.Context, mint solana.PublicKey) (uint8, error) {
	// rpcClient.GetTokenSupply parses the mint and returns decimals directly.
	supply, err := c.rpcClient.GetTokenSupply(ctx, mint, c.commitment)
	if err != nil {
		return 0, fmt.Errorf("getTokenSupply(%s): %w", mint, err)
	}
	if supply == nil || supply.Value == nil {
		return 0, fmt.Errorf("getTokenSupply(%s): nil response", mint)
	}
	return supply.Value.Decimals, nil
}

func (c *SolanaClient) BalanceOf(ctx context.Context, account, token ChainAddress) (*big.Int, error) {
	if account.Kind != "solana" || token.Kind != "solana" {
		return nil, &ErrUnsupported{Op: "BalanceOf", Kind: account.Kind + "/" + token.Kind}
	}
	if len(account.Bytes) != 32 || len(token.Bytes) != 32 {
		return nil, errors.New("solana addresses must be 32 bytes")
	}
	owner := solana.PublicKeyFromBytes(account.Bytes)
	mint := solana.PublicKeyFromBytes(token.Bytes)
	ata, _, err := solana.FindAssociatedTokenAddress(owner, mint)
	if err != nil {
		return nil, fmt.Errorf("derive ATA: %w", err)
	}
	bal, err := c.rpcClient.GetTokenAccountBalance(ctx, ata, c.commitment)
	if err != nil {
		return big.NewInt(0), nil
	}
	if bal == nil || bal.Value == nil {
		return big.NewInt(0), nil
	}
	out, ok := new(big.Int).SetString(bal.Value.Amount, 10)
	if !ok {
		return big.NewInt(0), nil
	}
	return out, nil
}

// PlaceBid signs and sends a place_bid instruction. Requires that the
// solver's signer keypair was provided to NewSolanaClient.
func (c *SolanaClient) PlaceBid(ctx context.Context, intentID, bidAmount *big.Int) (string, error) {
	if len(c.signer) == 0 {
		return "", errors.New("solana signer not configured (set SOLVER_SOLANA_PRIVATE_KEY)")
	}
	if intentID == nil || intentID.Sign() <= 0 {
		return "", errors.New("intent id must be positive")
	}
	if bidAmount == nil || bidAmount.Sign() <= 0 {
		return "", errors.New("bid amount must be positive")
	}
	ix, err := c.BuildPlaceBidIx(c.signer.PublicKey(), intentID.Uint64(), bidAmount.Uint64())
	if err != nil {
		return "", err
	}
	return c.sendIxs(ctx, []solana.Instruction{ix}, []solana.PrivateKey{c.signer})
}

// DepositAndPickup signs and sends a deposit_and_pickup. The current
// IntentRecord on-chain state is read first to pick up token_a_mint and
// derive the solver's source ATA.
func (c *SolanaClient) DepositAndPickup(ctx context.Context, intentID *big.Int) (string, error) {
	if len(c.signer) == 0 {
		return "", errors.New("solana signer not configured (set SOLVER_SOLANA_PRIVATE_KEY)")
	}
	snap, err := c.GetIntent(ctx, intentID)
	if err != nil {
		return "", err
	}
	if len(snap.TokenA.Bytes) != 32 {
		return "", errors.New("intent token_a_mint malformed on-chain")
	}
	tokenAMint := solana.PublicKeyFromBytes(snap.TokenA.Bytes)
	solverPub := c.signer.PublicKey()
	solverATA, _, err := solana.FindAssociatedTokenAddress(solverPub, tokenAMint)
	if err != nil {
		return "", err
	}
	ix, err := c.BuildDepositAndPickupIx(solverPub, tokenAMint, solverATA, intentID.Uint64())
	if err != nil {
		return "", err
	}
	return c.sendIxs(ctx, []solana.Instruction{ix}, []solana.PrivateKey{c.signer})
}

// SolveIntentOnChain2 signs and sends solve_intent_on_chain2. Recipient
// ATAs are pre-created idempotently in the same transaction so the solver
// pays rent for any that don't yet exist (per the design lock).
func (c *SolanaClient) SolveIntentOnChain2(
	ctx context.Context,
	srcChainID, srcIntentID *big.Int,
	user ChainAddress,
	tokenB ChainAddress,
	expectedAmountB *big.Int,
	recipients []ChainAddress,
	amounts []*big.Int,
) (string, error) {
	if len(c.signer) == 0 {
		return "", errors.New("solana signer not configured (set SOLVER_SOLANA_PRIVATE_KEY)")
	}
	if len(recipients) != len(amounts) {
		return "", errors.New("recipients/amounts length mismatch")
	}
	if len(tokenB.Bytes) != 32 {
		return "", errors.New("solana tokenB must be 32 bytes")
	}
	if len(user.Bytes) > 32 {
		return "", errors.New("user pubkey/addr must fit in 32 bytes")
	}
	var userBytes [32]byte
	copy(userBytes[32-len(user.Bytes):], user.Bytes) // right-align (EVM addr → bytes32 left-pad zeros)
	tokenBMint := solana.PublicKeyFromBytes(tokenB.Bytes)
	solverPub := c.signer.PublicKey()
	solverATA, _, err := solana.FindAssociatedTokenAddress(solverPub, tokenBMint)
	if err != nil {
		return "", err
	}

	amountsU := make([]uint64, len(amounts))
	recipientATAs := make([]solana.PublicKey, len(recipients))
	createATAIxs := make([]solana.Instruction, 0, len(recipients))
	for i, r := range recipients {
		if amounts[i] == nil || amounts[i].Sign() < 0 {
			return "", fmt.Errorf("amount[%d] must be non-negative", i)
		}
		amountsU[i] = amounts[i].Uint64()
		if len(r.Bytes) != 32 {
			return "", fmt.Errorf("recipient[%d] must be 32 bytes (Solana pubkey)", i)
		}
		owner := solana.PublicKeyFromBytes(r.Bytes)
		ata, _, err := solana.FindAssociatedTokenAddress(owner, tokenBMint)
		if err != nil {
			return "", fmt.Errorf("derive ATA[%d]: %w", i, err)
		}
		recipientATAs[i] = ata
		// Idempotent ATA create — a no-op if the ATA already exists; solver pays rent if it doesn't.
		createATAIxs = append(createATAIxs,
			atok.NewCreateIdempotentInstruction(solverPub, owner, tokenBMint).Build(),
		)
	}

	if expectedAmountB == nil || expectedAmountB.Sign() <= 0 {
		return "", errors.New("expectedAmountB must be positive")
	}
	solveIx, err := c.BuildSolveIntentOnChain2Ix(
		solverPub,
		tokenBMint,
		solverATA,
		srcChainID.Uint64(),
		srcIntentID.Uint64(),
		userBytes,
		expectedAmountB.Uint64(),
		amountsU,
		recipientATAs,
	)
	if err != nil {
		return "", err
	}

	allIxs := append(createATAIxs, solveIx)
	return c.sendIxs(ctx, allIxs, []solana.PrivateKey{c.signer})
}

// SettleIntent submits `settle_intent_with_chain2_verification` for a
// Solana-source intent. The caller (relayer) must hold an ed25519 key
// authorized on-chain via `add_relayer` (see migrations/deploy.ts).
//
// Reads the IntentRecord PDA to learn token_a_mint and winning_solver,
// derives the solver's ATA, builds + signs + sends the instruction.
//
// Returns the transaction signature.
func (c *SolanaClient) SettleIntent(
	ctx context.Context,
	intentID, chain2IntentID *big.Int,
) (string, error) {
	if len(c.signer) == 0 {
		return "", errors.New("solana signer not configured (relayer needs RELAYER_SOLANA_PRIVATE_KEY)")
	}
	if intentID == nil || intentID.Sign() <= 0 {
		return "", errors.New("intent id must be positive")
	}

	snap, err := c.GetIntent(ctx, intentID)
	if err != nil {
		return "", fmt.Errorf("read intent: %w", err)
	}
	if len(snap.TokenA.Bytes) != 32 || len(snap.WinningSolver.Bytes) != 32 {
		return "", errors.New("intent record bytes malformed")
	}
	tokenAMint := solana.PublicKeyFromBytes(snap.TokenA.Bytes)
	winningSolver := solana.PublicKeyFromBytes(snap.WinningSolver.Bytes)
	if winningSolver.IsZero() {
		return "", errors.New("intent has no winning solver yet — finalize_auction must run first")
	}
	winningSolverATA, _, err := solana.FindAssociatedTokenAddress(winningSolver, tokenAMint)
	if err != nil {
		return "", fmt.Errorf("derive winning solver ATA: %w", err)
	}

	chain2ID := uint64(0)
	if chain2IntentID != nil {
		chain2ID = chain2IntentID.Uint64()
	}

	ix, err := c.BuildSettleIntentIx(
		c.signer.PublicKey(),
		tokenAMint,
		winningSolver,
		winningSolverATA,
		intentID.Uint64(),
		chain2ID,
	)
	if err != nil {
		return "", err
	}
	return c.sendIxs(ctx, []solana.Instruction{ix}, []solana.PrivateKey{c.signer})
}

// FinalizeAuction submits `finalize_auction` once the auction window has
// closed. Anyone can call this; the solver does so for intents it bid on
// to advance them into the IntentWon state.
func (c *SolanaClient) FinalizeAuction(ctx context.Context, intentID *big.Int) (string, error) {
	if len(c.signer) == 0 {
		return "", errors.New("solana signer not configured")
	}
	if intentID == nil || intentID.Sign() <= 0 {
		return "", errors.New("intent id must be positive")
	}
	ix, err := c.BuildFinalizeAuctionIx(c.signer.PublicKey(), intentID.Uint64())
	if err != nil {
		return "", err
	}
	return c.sendIxs(ctx, []solana.Instruction{ix}, []solana.PrivateKey{c.signer})
}

// Close releases the websocket subscription if open.
func (c *SolanaClient) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.wsClient != nil {
			c.wsClient.Close()
			c.wsClient = nil
		}
	})
	return nil
}

// ----- PDA helpers -----

func (c *SolanaClient) intentRecordPDA(intentID *big.Int) (solana.PublicKey, uint8, error) {
	if intentID == nil || intentID.Sign() <= 0 {
		return solana.PublicKey{}, 0, errors.New("intent id must be positive")
	}
	seedID := u64ToLE(intentID.Uint64())
	pda, bump, err := solana.FindProgramAddress(
		[][]byte{[]byte("intent"), seedID},
		c.programID,
	)
	return pda, bump, err
}

func (c *SolanaClient) solvedRecordPDA(srcChainID, srcIntentID *big.Int) (solana.PublicKey, uint8, error) {
	if srcChainID == nil || srcChainID.Sign() <= 0 {
		return solana.PublicKey{}, 0, errors.New("src chain id must be positive")
	}
	if srcIntentID == nil || srcIntentID.Sign() <= 0 {
		return solana.PublicKey{}, 0, errors.New("src intent id must be positive")
	}
	pda, bump, err := solana.FindProgramAddress(
		[][]byte{
			[]byte("solved"),
			u64ToLE(srcChainID.Uint64()),
			u64ToLE(srcIntentID.Uint64()),
		},
		c.programID,
	)
	return pda, bump, err
}

// StealthPDA derives the stealth registration PDA from the user's pubkey
// and the relayer-issued ephemeral salt. Mirrors the Anchor program seeds.
func (c *SolanaClient) StealthPDA(user solana.PublicKey, ephemeralSalt [32]byte) (solana.PublicKey, uint8, error) {
	pda, bump, err := solana.FindProgramAddress(
		[][]byte{[]byte("stealth"), user.Bytes(), ephemeralSalt[:]},
		c.programID,
	)
	return pda, bump, err
}

// ConfigPDA returns the singleton Config PDA for this program.
func (c *SolanaClient) ConfigPDA() (solana.PublicKey, uint8, error) {
	return solana.FindProgramAddress([][]byte{[]byte("config")}, c.programID)
}

// EscrowAuthorityPDA returns the singleton escrow authority PDA whose ATAs
// hold all source-side escrowed funds.
func (c *SolanaClient) EscrowAuthorityPDA() (solana.PublicKey, uint8, error) {
	return solana.FindProgramAddress([][]byte{[]byte("escrow")}, c.programID)
}

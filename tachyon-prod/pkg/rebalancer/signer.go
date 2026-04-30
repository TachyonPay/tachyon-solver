package rebalancer

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// EOASigner is a single-private-key signer used for early rebalancer testing.
//
// It deliberately does NOT go through a Safe. The intent is to validate the
// executor flow with a small-funds key on mainnet before promoting the
// rebalancer to the production Safe-signing path.
//
// The signer reuses the per-chain *ethclient.Client owned by the solver's
// blockchain.Client (so we don't double-dial RPCs) but signs/sends with its
// own ECDSA key.
type EOASigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
	chains     map[string]chainCtx
	mu         sync.Mutex // serialize tx sends per process; avoids local nonce races
}

type chainCtx struct {
	eth     *ethclient.Client
	chainID *big.Int
}

// NewEOASignerFromEnv constructs an EOASigner from REBALANCER_TEST_PRIVATE_KEY,
// using the same RPC endpoints the solver uses.
//
// Returns (nil, nil) when REBALANCER_TEST_PRIVATE_KEY is unset — callers
// should treat this as "no live executor available; dry-run only".
func NewEOASignerFromEnv(clients map[string]*blockchain.Client) (*EOASigner, error) {
	hexKey := strings.TrimPrefix(os.Getenv("REBALANCER_TEST_PRIVATE_KEY"), "0x")
	if hexKey == "" {
		return nil, nil
	}
	priv, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid REBALANCER_TEST_PRIVATE_KEY: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	chains := make(map[string]chainCtx, len(clients))
	for chainID, c := range clients {
		chains[chainID] = chainCtx{eth: c.EthClient(), chainID: c.ChainID()}
	}

	log.WithFields(log.Fields{
		"address": addr.Hex(),
		"chains":  len(chains),
	}).Info("Rebalancer EOA test signer initialized")

	return &EOASigner{privateKey: priv, address: addr, chains: chains}, nil
}

// Address returns the EOA's address.
func (s *EOASigner) Address() common.Address { return s.address }

// chain looks up the chain context or returns a friendly error.
func (s *EOASigner) chain(chainID string) (chainCtx, error) {
	cc, ok := s.chains[chainID]
	if !ok {
		return chainCtx{}, fmt.Errorf("no rpc client for chainID %s", chainID)
	}
	return cc, nil
}

// SendTx builds, signs, and submits an EIP-1559 transaction. It does NOT wait
// for inclusion — call WaitMined separately.
func (s *EOASigner) SendTx(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64) (*types.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cc, err := s.chain(chainID)
	if err != nil {
		return nil, err
	}

	nonce, err := cc.eth.PendingNonceAt(ctx, s.address)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	tip, err := cc.eth.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("tip cap: %w", err)
	}
	head, err := cc.eth.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)

	if gasLimit == 0 {
		// Fallback estimate. Doubled for safety on multi-step calls.
		est, err := cc.eth.EstimateGas(ctx, callMsgFromTx(s.address, to, value, data))
		if err != nil {
			return nil, fmt.Errorf("estimate gas: %w", err)
		}
		gasLimit = est * 12 / 10
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   cc.chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(cc.chainID), s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	if err := cc.eth.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	return signed, nil
}

// SendAndWait sends and blocks until the receipt is available; returns the
// receipt status (1 = success). Caller can pass 0 gasLimit to estimate.
func (s *EOASigner) SendAndWait(ctx context.Context, chainID string, to common.Address, value *big.Int, data []byte, gasLimit uint64, timeout time.Duration) (*types.Transaction, *types.Receipt, error) {
	tx, err := s.SendTx(ctx, chainID, to, value, data, gasLimit)
	if err != nil {
		return nil, nil, err
	}
	cc, err := s.chain(chainID)
	if err != nil {
		return tx, nil, err
	}
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rcpt, err := bind.WaitMined(waitCtx, cc.eth, tx)
	if err != nil {
		return tx, nil, fmt.Errorf("wait mined %s: %w", tx.Hash().Hex(), err)
	}
	if rcpt.Status != 1 {
		return tx, rcpt, fmt.Errorf("tx %s reverted (status %d)", tx.Hash().Hex(), rcpt.Status)
	}
	return tx, rcpt, nil
}

// CallReadOnly executes an `eth_call` with the EOA as msg.sender.
func (s *EOASigner) CallReadOnly(ctx context.Context, chainID string, to common.Address, data []byte) ([]byte, error) {
	cc, err := s.chain(chainID)
	if err != nil {
		return nil, err
	}
	msg := callMsgFromTx(s.address, to, big.NewInt(0), data)
	return cc.eth.CallContract(ctx, msg, nil)
}

// EthClient exposes the per-chain RPC client (useful for things like
// SuggestGasPrice on rare paths).
func (s *EOASigner) EthClient(chainID string) (*ethclient.Client, error) {
	cc, err := s.chain(chainID)
	if err != nil {
		return nil, err
	}
	return cc.eth, nil
}

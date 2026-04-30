// fund-test-safe transfers ALL token balances (USDC, ZEN, WETH on Base;
// USDC.e, ZEN on Horizen) from the EOA (PRIVATE_KEY) to the test Safe on each
// chain. Native gas (ETH on Base, ZEN-gas on Horizen) is left on the EOA so
// the relayer can keep paying outer execTransaction gas.
//
// Usage:
//   cd tachyon-prod && go run ./cmd/fund-test-safe/
package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

type token struct {
	name     string
	addr     common.Address
	decimals int
}

type chainSpec struct {
	name    string
	rpc     string
	chainID *big.Int
	safe    common.Address
	tokens  []token
}

var chains = []chainSpec{
	{
		name:    "base",
		rpc:     "https://base-mainnet.g.alchemy.com/v2/ZnxNrK14O1olpqoj-KVkP",
		chainID: big.NewInt(8453),
		safe:    common.HexToAddress("0xa4A8E2dDA54321E257615fef9FF467401D1C17D4"),
		tokens: []token{
			{name: "USDC", addr: common.HexToAddress("0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"), decimals: 6},
			{name: "ZEN ", addr: common.HexToAddress("0xf43eB8De897Fbc7F2502483B2Bef7Bb9EA179229"), decimals: 18},
			{name: "WETH", addr: common.HexToAddress("0x4200000000000000000000000000000000000006"), decimals: 18},
		},
	},
	{
		name:    "horizen",
		rpc:     "https://horizen.calderachain.xyz/http",
		chainID: big.NewInt(26514),
		safe:    common.HexToAddress("0x71ca2818D92123dfC7072486F73aDBF5F1E28974"),
		tokens: []token{
			{name: "USDC.e", addr: common.HexToAddress("0xDF7108f8B10F9b9eC1aba01CCa057268cbf86B6c"), decimals: 6},
			{name: "ZEN  ", addr: common.HexToAddress("0x57da2D504bf8b83Ef304759d9f2648522D7a9280"), decimals: 18},
		},
	},
}

// ERC20 selectors
var (
	transferSel  = []byte{0xa9, 0x05, 0x9c, 0xbb} // transfer(address,uint256)
	balanceOfSel = []byte{0x70, 0xa0, 0x82, 0x31} // balanceOf(address)
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		fmt.Printf("warn: no .env loaded: %v\n", err)
	}

	pkHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pkHex == "" {
		fatal("PRIVATE_KEY not set")
	}
	priv, err := crypto.HexToECDSA(pkHex)
	if err != nil {
		fatal(fmt.Sprintf("invalid PRIVATE_KEY: %v", err))
	}
	from := crypto.PubkeyToAddress(priv.PublicKey)
	ctx := context.Background()

	fmt.Printf("EOA: %s\n\n", from.Hex())

	for _, ch := range chains {
		fmt.Printf("=== %s (Safe %s) ===\n", ch.name, ch.safe.Hex())
		client, err := ethclient.Dial(ch.rpc)
		if err != nil {
			fatal(fmt.Sprintf("dial %s: %v", ch.name, err))
		}

		for _, tk := range ch.tokens {
			bal, err := readBalance(ctx, client, tk.addr, from)
			if err != nil {
				fmt.Printf("  %s: balance read failed: %v\n", tk.name, err)
				continue
			}
			if bal.Sign() == 0 {
				fmt.Printf("  %s: zero balance, skipping\n", tk.name)
				continue
			}
			fmt.Printf("  %s: %s raw → transferring all to Safe...\n", tk.name, bal.String())

			data := append([]byte{}, transferSel...)
			data = append(data, common.LeftPadBytes(ch.safe.Bytes(), 32)...)
			data = append(data, common.LeftPadBytes(bal.Bytes(), 32)...)

			tx, err := signAndSend(ctx, client, ch.chainID, priv, from, &tk.addr, big.NewInt(0), data, 100_000)
			if err != nil {
				fatal(fmt.Sprintf("    transfer %s: %v", tk.name, err))
			}
			fmt.Printf("    tx %s — waiting...\n", tx.Hash().Hex())
			if err := waitMined(ctx, client, tx); err != nil {
				fatal(fmt.Sprintf("    %s tx failed: %v", tk.name, err))
			}

			newBal, _ := readBalance(ctx, client, tk.addr, ch.safe)
			fmt.Printf("    confirmed. Safe %s balance now %s\n", tk.name, newBal.String())
		}
		fmt.Println()
	}

	fmt.Println("Done. EOA retains all native gas (ETH on Base, ZEN-gas on Horizen) for relayer outer txs.")
}

func readBalance(ctx context.Context, client *ethclient.Client, token, holder common.Address) (*big.Int, error) {
	data := append([]byte{}, balanceOfSel...)
	data = append(data, common.LeftPadBytes(holder.Bytes(), 32)...)
	out, err := client.CallContract(ctx, ethereum.CallMsg{From: holder, To: &token, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(out), nil
}

func signAndSend(
	ctx context.Context,
	client *ethclient.Client,
	chainID *big.Int,
	priv *ecdsa.PrivateKey,
	from common.Address,
	to *common.Address,
	value *big.Int,
	data []byte,
	gasLimit uint64,
) (*types.Transaction, error) {
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	tip, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("tip: %w", err)
	}
	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        to,
		Value:     value,
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	return signed, nil
}

func waitMined(ctx context.Context, client *ethclient.Client, tx *types.Transaction) error {
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rcpt, err := bind.WaitMined(waitCtx, client, tx)
	if err != nil {
		return err
	}
	if rcpt.Status != 1 {
		return fmt.Errorf("tx %s reverted", tx.Hash().Hex())
	}
	return nil
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s\n", msg)
	os.Exit(1)
}

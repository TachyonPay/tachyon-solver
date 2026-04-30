package rebalancer

import (
	"context"
	"fmt"
	"math/big"

	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// Snapshot is a point-in-time view of all configured (chain, token) balances.
type Snapshot struct {
	Balances map[Key]*big.Int // raw token units
	Targets  map[Key]*big.Int // raw token units, copied from cfg for convenience
}

// TakeSnapshot reads the current balance for every (chain, token) for which
// a target is configured. It uses each chain's existing blockchain.Client,
// which already routes through the Safe address when a Safe is wired.
func TakeSnapshot(ctx context.Context, cfg *Config, clients map[string]*blockchain.Client) (*Snapshot, error) {
	out := &Snapshot{
		Balances: make(map[Key]*big.Int, len(cfg.Targets)),
		Targets:  make(map[Key]*big.Int, len(cfg.Targets)),
	}

	for k, target := range cfg.Targets {
		client, ok := clients[k.ChainID]
		if !ok {
			return nil, fmt.Errorf("no blockchain client for chain %s", k.ChainID)
		}
		tokenAddr := TokenAddress(k.ChainID, k.Token)
		bal, err := client.GetBalance(ctx, tokenAddr)
		if err != nil {
			return nil, fmt.Errorf("get balance %s: %w", k, err)
		}
		out.Balances[k] = bal
		out.Targets[k] = new(big.Int).Set(target)
	}

	return out, nil
}

// Bucket reports how a single (chain, token) balance compares to its target.
type Bucket struct {
	Key       Key
	Balance   *big.Int
	Target    *big.Int
	Threshold *big.Int // ThresholdPct * Target — below this is a deficit
	Floor     *big.Int // (1 - BufferPct) * Target — sources can't be drained below this
	Deficit   *big.Int // max(0, Target - Balance) — only set when Balance < Threshold
	Surplus   *big.Int // max(0, Balance - Floor)  — how much is safely available to send out
}

// Buckets enumerates every snapshot entry as a Bucket with deficit/surplus computed.
func (s *Snapshot) Buckets(cfg *Config) []Bucket {
	out := make([]Bucket, 0, len(s.Balances))
	for k, target := range s.Targets {
		bal := s.Balances[k]
		threshold := pctOf(target, cfg.ThresholdPct)
		floor := pctOf(target, 1.0-cfg.BufferPct)

		b := Bucket{
			Key: k, Balance: new(big.Int).Set(bal), Target: new(big.Int).Set(target),
			Threshold: threshold, Floor: floor,
			Deficit: big.NewInt(0), Surplus: big.NewInt(0),
		}
		if bal.Cmp(threshold) < 0 {
			b.Deficit = new(big.Int).Sub(target, bal)
		}
		if bal.Cmp(floor) > 0 {
			b.Surplus = new(big.Int).Sub(bal, floor)
		}
		out = append(out, b)
	}
	return out
}

// pctOf returns floor(amount * pct) using big.Float.
func pctOf(amount *big.Int, pct float64) *big.Int {
	if amount.Sign() == 0 {
		return big.NewInt(0)
	}
	f := new(big.Float).Mul(new(big.Float).SetInt(amount), big.NewFloat(pct))
	out, _ := f.Int(nil)
	return out
}

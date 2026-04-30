package rebalancer

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

// Rebalancer is the entry-point used by the solver/api layer. It owns the
// config and a reference to the chain clients, but executors are pluggable
// so we can ship the dry-run path before the live executors.
type Rebalancer struct {
	cfg      *Config
	clients  map[string]*blockchain.Client
	executor Executor // nil = dry-run only
	quoter   Quoter   // nil = planner falls back to heuristics
}

// Executor runs a single Action on-chain. Returning an error halts the plan
// for safety; the caller should re-snapshot before retrying.
type Executor interface {
	Execute(ctx context.Context, a Action) (txHash string, err error)
}

func New(cfg *Config, clients map[string]*blockchain.Client) *Rebalancer {
	return &Rebalancer{cfg: cfg, clients: clients}
}

// SetExecutor wires a live executor. When nil, ExecutePlan only logs.
func (r *Rebalancer) SetExecutor(e Executor) { r.executor = e }

// SetQuoter wires a swap quoter that the planner uses to size bridge legs
// that fund a same-chain swap. Optional — falls back to a hardcoded heuristic
// when nil.
func (r *Rebalancer) SetQuoter(q Quoter) { r.quoter = q }

// Config returns the loaded configuration.
func (r *Rebalancer) Config() *Config { return r.cfg }

// DryRun snapshots balances, builds a plan, and returns it without executing.
func (r *Rebalancer) DryRun(ctx context.Context) (*Snapshot, *Plan, error) {
	if !r.cfg.Enabled {
		return nil, nil, fmt.Errorf("rebalancer disabled (set REBALANCER_ENABLED=true)")
	}
	snap, err := TakeSnapshot(ctx, r.cfg, r.clients)
	if err != nil {
		return nil, nil, err
	}
	plan := BuildPlan(ctx, snap, r.cfg, r.quoter)

	log.WithFields(log.Fields{
		"actionCount": len(plan.Actions),
		"noteCount":   len(plan.Notes),
	}).Info("Rebalancer dry-run complete")
	for i, a := range plan.Actions {
		log.WithFields(log.Fields{
			"step":   i + 1,
			"kind":   a.Kind,
			"src":    a.SrcChainID,
			"dst":    a.DstChainID,
			"amount": a.Amount.String(),
		}).Info(a.Reason)
	}
	for _, n := range plan.Notes {
		log.WithField("note", n).Warn("Rebalancer note")
	}

	return snap, plan, nil
}

// Execute runs a fresh plan and dispatches each action to the executor.
// If no executor is wired it behaves like DryRun.
func (r *Rebalancer) Execute(ctx context.Context) (*Snapshot, *Plan, []string, error) {
	snap, plan, err := r.DryRun(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if r.executor == nil {
		return snap, plan, nil, fmt.Errorf("no executor wired — call DryRun or attach an executor")
	}

	txHashes := make([]string, 0, len(plan.Actions))
	for i, a := range plan.Actions {
		hash, err := r.executor.Execute(ctx, a)
		if err != nil {
			return snap, plan, txHashes, fmt.Errorf("step %d (%s): %w", i+1, a.Kind, err)
		}
		txHashes = append(txHashes, hash)
		log.WithFields(log.Fields{
			"step":   i + 1,
			"kind":   a.Kind,
			"txHash": hash,
		}).Info("Rebalance step executed")
	}
	return snap, plan, txHashes, nil
}

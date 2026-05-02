#!/bin/bash
#
# three_bridges/run.sh — exercise all three bridge approaches from Base
# end-to-end with small live USDC amounts. Each path is sequential; on
# the first failure the script aborts with set -e.
#
#   1. tachyon       Base USDC  →  Horizen USDC.e  (solver intent auction)
#   2. lifiXtach     Base USDC  →  MegaETH USDT0   (privacy router via LI.FI Across)
#   3. stargateXtach Base USDC  →  Horizen USDC.e  (privacy router via Stargate v2)
#
# Defaults to 1 USDC per path (= 1_000_000 atomic, decimals=6). Override
# with TEST_AMOUNT_TACHYON / TEST_AMOUNT_LIFI / TEST_AMOUNT_STARGATE.
#
# Liquidity checklist before running:
#   - User EOA Base USDC  ≥ sum of the three test amounts
#   - Solver Safe Horizen USDC.e ≥ tachyon test amount (solver settlement)
#
# Default targets are the live deploy on tachyon.pe; override RELAYER_URL
# / PRIVACY_URL if you want to point at a local stack.
#
# Run from anywhere:
#
#   ./tachyon-prod/test/e2e/three_bridges/run.sh

set -euo pipefail

# tachyon-prod/test/e2e is its own Go module, so each `go run` has to be
# invoked from inside that directory. We cd in once here.
cd "$(dirname "$0")/.."

export RELAYER_URL="${RELAYER_URL:-https://relayer.tachyon.pe}"
export PRIVACY_URL="${PRIVACY_URL:-https://privacy.tachyon.pe}"

GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

banner() {
  echo
  echo -e "${BOLD}${CYAN}=========================================================${NC}"
  echo -e "${BOLD}${CYAN}$1${NC}"
  echo -e "${BOLD}${CYAN}=========================================================${NC}"
}

step_done() {
  echo -e "${GREEN}✓ $1 complete${NC}\n"
}

# atomicToUsdc converts a 6-decimals atomic uint into a printable USDC
# string (e.g. "1.0000"). Uses awk -v to avoid quote-escape gymnastics.
atomicToUsdc() {
  awk -v a="$1" 'BEGIN { printf "%.4f", a/1000000 }'
}

# ---------- 1. tachyon — solver intent auction (both directions) ----------
# Default 0.05 USDC because the operator wallet's Horizen USDC.e balance
# is small; complete_flow runs both legs (Horizen→Base and Base→Horizen)
# at the same amount.
TEST_AMOUNT_TACHYON="${TEST_AMOUNT_TACHYON:-50000}"   # 0.05 USDC

banner "1/3 — tachyon (USDC ↔ USDC.e bidirectional via solver auction)"
echo "  amount per leg: $TEST_AMOUNT_TACHYON atomic ($(atomicToUsdc "$TEST_AMOUNT_TACHYON") USDC)"
echo "  ETA: ~5 minutes."
echo

# Bid spread: 0.95 USDC out for 1 USDC in + 0.05 USDC reward to solver.
# Recipient amounts MUST sum to AmountB or the Bridge.solveIntent call
# reverts on the dest chain (the contract checks sum of recipient
# amounts == bid). Default 30/35/30 = 95 splits scale by AmountB/95.
A=$(atomicToUsdc "$TEST_AMOUNT_TACHYON")
B=$(awk -v a="$TEST_AMOUNT_TACHYON" 'BEGIN { printf "%.6f", (a*95/100)/1000000 }')
R=$(awk -v a="$TEST_AMOUNT_TACHYON" 'BEGIN { printf "%.6f", (a*5/100)/1000000 }')
R1=$(awk -v b="$B" 'BEGIN { printf "%.6f", b*30/95 }')
R2=$(awk -v b="$B" 'BEGIN { printf "%.6f", b*35/95 }')
R3=$(awk -v b="$B" 'BEGIN { printf "%.6f", b - (b*30/95) - (b*35/95) }')

TEST_AMOUNT_A="$A" \
TEST_AMOUNT_B="$B" \
TEST_REWARD="$R" \
TEST_RECIPIENT_1_AMOUNT="$R1" \
TEST_RECIPIENT_2_AMOUNT="$R2" \
TEST_RECIPIENT_3_AMOUNT="$R3" \
go run ./complete_flow.go

step_done "tachyon"

# ---------- 2. lifiXtach — privacy router via LI.FI Across ----------
TEST_AMOUNT_LIFI="${TEST_AMOUNT_LIFI:-1000000}"

banner "2/3 — lifiXtach (Base USDC → MegaETH USDT0 via LI.FI/Across)"
echo "  amount: $TEST_AMOUNT_LIFI atomic ($(atomicToUsdc "$TEST_AMOUNT_LIFI") USDC)"

PRIVACY_TEST_AMOUNT="$TEST_AMOUNT_LIFI" \
PRIVACY_TEST_DEST_CHAIN=4326 \
PRIVACY_TEST_DEST_TOKEN=USDT0 \
go run ./privacy_flow

step_done "lifiXtach"

# ---------- 3. stargateXtach — privacy router via Stargate v2 ----------
TEST_AMOUNT_STARGATE="${TEST_AMOUNT_STARGATE:-1000000}"

banner "3/3 — stargateXtach (Base USDC → Horizen USDC.e via Stargate)"
echo "  amount: $TEST_AMOUNT_STARGATE atomic ($(atomicToUsdc "$TEST_AMOUNT_STARGATE") USDC)"

PRIVACY_TEST_AMOUNT="$TEST_AMOUNT_STARGATE" \
PRIVACY_TEST_DEST_CHAIN=26514 \
PRIVACY_TEST_DEST_TOKEN=USDC.E \
go run ./privacy_flow

step_done "stargateXtach"

echo
echo -e "${BOLD}${GREEN}All three bridges delivered successfully.${NC}"

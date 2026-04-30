# Rebalancer

Cross-chain inventory rebalancer for the Tachyon solver. Detects token deficits
on Base and Horizen mainnet and orchestrates bridges + DEX swaps to bring
balances back to operator-defined targets.

## Quick start

The rebalancer is **disabled by default**. To turn it on, set:

```env
REBALANCER_ENABLED=true
```

Then either dry-run only (no on-chain writes) or wire one of the executor
modes below.

## Run modes

The rebalancer runs in one of three modes, picked at startup:

| Mode | Trigger | Signs with | Funds held by | Use case |
|---|---|---|---|---|
| **Dry-run only** | `REBALANCER_ENABLED=true` and neither flag below | n/a | n/a | inspect plans without executing |
| **EOA test** | `REBALANCER_TEST_PRIVATE_KEY=0x…` | the test key (single private key) | the test key | small-funds smoke test on mainnet |
| **Safe (production)** | `REBALANCER_USE_SAFE=true` | 3 TEE keys via Gnosis Safe | the Safe contract | production |

Safe mode takes precedence if both flags are set.

## Required env vars

### Always required (when `REBALANCER_ENABLED=true`)

At least one `TARGET_<TOKEN>_<CHAIN>` must be set. Values are raw token units
(USDC has 6 decimals; ZEN and WETH have 18).

```env
REBALANCER_ENABLED=true

# Targets — set the (token, chain) buckets you want maintained.
# Omit any combination that's not relevant.
TARGET_USDC_BASE=10000000000              # 10,000 USDC on Base
TARGET_USDC_HORIZEN=5000000000            # 5,000 USDC.e on Horizen
TARGET_ZEN_BASE=20000000000000000000      # 20 ZEN on Base
TARGET_ZEN_HORIZEN=20000000000000000000   # 20 ZEN on Horizen
TARGET_WETH_BASE=1000000000000000000      # 1 WETH on Base (Horizen has no WETH)
```

### Optional knobs (with defaults)

```env
# Trigger when balance < threshold_pct * target. Default 30%.
REBALANCER_THRESHOLD_PCT=0.30

# Don't drain a source bucket below (1 - buffer_pct) * target. Default 10%.
REBALANCER_BUFFER_PCT=0.10

# Slippage cap for bridges and swaps. Default 1%.
REBALANCER_SLIPPAGE_PCT=0.01
```

`REBALANCER_STARGATE_GAS_BUFFER_WEI` and `REBALANCER_OFT_GAS_BUFFER_WEI` exist
in the schema but are **intentionally unused** — Stargate V2 reverts with
`Stargate_InvalidAmount()` if `msg.value != fee.NativeFee` exactly, so we always
pass the quoted fee verbatim.

### Mode 1 — EOA test

```env
REBALANCER_TEST_PRIVATE_KEY=0xe0a7…       # any private key, hex-encoded
```

This is the bootstrap path used to validate behavior with a small-funds wallet
before promoting to Safe mode. The same key signs all txs and pays all gas. No
Safe involvement.

### Mode 2 — Safe (production)

```env
REBALANCER_USE_SAFE=true
```

Reuses the **already-configured Safe wiring** from the solver. The rebalancer
shares these env vars with the solver — you do not need to duplicate them:

```env
# Per-chain Safe address — same Safe (same address via CREATE2) on each chain
CHAIN_3_SAFE_ADDRESS=0x…                  # Horizen mainnet
CHAIN_4_SAFE_ADDRESS=0x…                  # Base mainnet

# 3 TEE-held private keys that co-sign every Safe tx. All 3 sign each tx;
# signatures are sorted by owner address ascending before submission.
SAFE_SIGNER_KEY_1=…                       # owner private key 1, no 0x prefix
SAFE_SIGNER_KEY_2=…
SAFE_SIGNER_KEY_3=…

# EOA that pays gas for the outer execTransaction call. Does NOT need to be
# a Safe owner; only needs ETH on Base and ZEN on Horizen.
PRIVATE_KEY=…
```

Funds (USDC, ZEN, WETH) and approvals live on the Safe. The Safe also needs
**native gas** (ETH on Base, ZEN on Horizen) to pay LayerZero / Stargate
messaging fees on the inner call.

## Token & route addresses (hardcoded)

These are baked into `pkg/rebalancer/addresses.go`. Listed here for visibility:

| | Base mainnet (8453) | Horizen mainnet (26514) |
|---|---|---|
| **USDC / USDC.e** | `0x833589fcd6edb6e08f4c7c32d4f71b54bda02913` | `0xDF7108f8B10F9b9eC1aba01CCa057268cbf86B6c` |
| **WETH** | `0x4200000000000000000000000000000000000006` | (not deployed) |
| **ZEN ERC20** | `0xf43eB8De897Fbc7F2502483B2Bef7Bb9EA179229` | `0x57da2D504bf8b83Ef304759d9f2648522D7a9280` |
| **LZ EndpointV2** | `0x1a44076050125825900e736c501f859c50fE728c` | `0x6F475642a6e85809B1c36Fa62763669b1b48DD5B` |
| **LZ EID** | 30184 | 30399 |
| **Stargate Pool USDC** | `0x27a16dc786820B16E5c9028b75B99F6f604b5d26` | `0x3a1293Bdb83bBbDd5Ebf4fAc96605aD2021BbC0f` |
| **ZEN OFT** | `0x57da2D504bf8b83Ef304759d9f2648522D7a9280` (OFTAdapter wrapping ERC20) | `0x57da2D504bf8b83Ef304759d9f2648522D7a9280` (NativeOFT, same addr) |
| **Aerodrome V2 Router** | `0xcF77a3Ba9A5CA399B7c97c74d54e5b1Beb874E43` | — |
| **Aerodrome V2 Factory** | `0x420DD381b31aEf6683db6B902084cB0FFECe40Da` | — |
| **Slipstream SwapRouter** | `0xBE6D8f0d05cC4be24d5167a3eF062215bE6D18a5` | — |
| **Slipstream QuoterV2** | `0x254cF9E1E6e233aa1AC962CB9B05b2cfeAaE15b0` | — |

## Admin endpoints

All endpoints require the `X-API-Key` header matching `ADMIN_API_KEY`.

| Endpoint | Purpose |
|---|---|
| `GET /admin/rebalance/snapshot` | Current balances + computed deficits/surpluses per bucket |
| `GET /admin/rebalance/dry-run` | Full plan: actions + reasons + soft notes. No tx submitted. |
| `POST /admin/rebalance/execute` | Snapshot + plan + execute via the wired executor |

Example:

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:3001/admin/rebalance/dry-run | jq
curl -X POST -H "X-API-Key: $ADMIN_API_KEY" http://localhost:3001/admin/rebalance/execute | jq
```

## Coverage matrix

The planner handles every combination of (deficit bucket, available source).
See `pkg/rebalancer/plan.go` for the routing logic and `plan_test.go` for the
17 unit tests asserting it.

| Deficit | Source order tried |
|---|---|
| USDC@Horizen | (1) USDC@Base bridge → (2) ZEN@Base swap+bridge → else Note |
| USDC@Base | (1) USDC@Horizen bridge → (2) ZEN@Base swap → else Note |
| ZEN@Horizen | (1) ZEN@Base bridge → (2) USDC@Base swap+bridge → else Note |
| ZEN@Base | (1) ZEN@Horizen bridge → (2) USDC@Base swap → (3) USDC@Horizen bridge+swap → else Note |
| WETH@Base | (1) USDC@Base swap → (2) ZEN@Base swap → (3) USDC@Horizen bridge+swap → (4) ZEN@Horizen bridge+swap → else Note |

## Testing

### Unit tests (planner logic, mock snapshots)

```bash
go test -run TestPlan_ -v ./pkg/rebalancer
```

17 tests, runs in <1s. No RPC calls.

### E2E test (live mainnet)

```bash
# Dry-run (read-only):
REBALANCER_E2E=1 \
PRIVATE_KEY=0x…                          \
go test -tags=e2e -run TestRebalancerE2E -v -timeout 3m ./pkg/rebalancer

# Execute (submits real tx):
REBALANCER_E2E=1 REBALANCER_E2E_EXECUTE=1 \
PRIVATE_KEY=0x…                          \
go test -tags=e2e -run TestRebalancerE2E -v -timeout 15m ./pkg/rebalancer
```

The e2e test uses the EOA path with the `PRIVATE_KEY` you provide. To test
Safe mode end-to-end, deploy and exercise via the admin API on a staging
solver instance with `REBALANCER_USE_SAFE=true`.

## Operational notes

- **Bridge arrival polling**: every bridge waits up to 10 minutes for the
  destination balance to grow by `minAmountLD` before returning. Chained
  swap-after-bridge actions therefore see fresh funds.
- **Sequential execution**: `/admin/rebalance/execute` runs actions one at a
  time. If a step fails, the call returns the partial tx list and stops; safe
  to re-run after fixing the underlying issue (re-snapshots first).
- **Safe needs native gas**: in Safe mode, the Safe (not the gas-paying EOA)
  funds the LayerZero messaging fee on bridges. Make sure the Safe holds
  enough ETH on Base and ZEN on Horizen.
- **Stargate fee parity**: Stargate V2's `send` reverts unless
  `msg.value == fee.NativeFee` exactly. The rebalancer always queries
  `quoteSend` immediately before submitting and forwards the quoted fee
  verbatim — no buffer.
- **Live quoter**: when an executor is wired, the planner uses Aerodrome's
  on-chain quoter to size bridge legs that fund swaps. This is ~2–3× more
  efficient than the hardcoded heuristic. Falls back to the heuristic if the
  quoter is unavailable.

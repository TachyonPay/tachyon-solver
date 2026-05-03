# Polling Guide — Transaction Status

How to poll the relayer for transaction status across all chains and assets, including per-chain timing expectations and a failure-handling strategy.

## Endpoint

```http
GET https://relayer.tachyon.pe/transactions?status=&page=1&limit=20
X-Signature: 0x<sig>
X-Timestamp: <unix_seconds>
```

Wallet-auth: client signs the literal string `tachyon-auth:<unix_timestamp>` with the user's wallet, sends the recovered hex sig + the same timestamp in the headers. 30-min validity.

For a single intent, filter the returned list client-side by `intentId`.

## Status Lifecycle

The `TransactionRecord.status` field has three values you'll see:

| status | meaning |
|---|---|
| `pending` | Record exists; bridge is in-flight |
| `completed` | Cross-chain delivery confirmed; tx hashes populated |
| `failed` | Unrecoverable (privacy bridges only — surfaces a refund opportunity) |

For a given `intentId`, the lifecycle is monotonic: once you see `completed` (or `failed`), no further updates land. Stop polling.

## Tx Hash Mapping

Three hash fields on the record. They're populated progressively for tachyon (each step writes its own hash on chain) and atomically at delivery time for privacy bridges.

| Field | Tachyon meaning | Privacy bridge meaning |
|---|---|---|
| `depositTxHash` | user → Bridge contract on source chain | user EOA → TEE Safe (sweep) |
| `solveTxHash` | solver pays out on dest chain | TEE Safe → LI.FI / Stargate router |
| `settleTxHash` | solver claims source deposit | arrival on dest chain |

For privacy bridges, all three populate together when the record transitions to `completed`. For tachyon, they appear in order across the bridge lifecycle.

## ⚠️ Privacy bridges + relayer pending visibility

**Today, privacy bridges only land on the relayer when they reach `delivered`.** Until then, polling `/transactions` will not show them.

Two ways the FE can surface "in-flight" privacy bridges:

### Option A (recommended now): Pre-create the pending record at submit time

After submitting the encrypted intent and getting back a `sessionId`, immediately POST a pending record to the relayer with the same wallet signature you'd use for queries:

```http
POST https://relayer.tachyon.pe/transactions
X-Signature: 0x<sig>
X-Timestamp: <unix_seconds>
Content-Type: application/json

{
  "intentId":        "<sessionId>",
  "userAddress":     "<user wallet>",
  "sourceChainId":   "8453",
  "destChainId":     "4326",
  "tokenA":          "USDC",
  "tokenB":          "USDT0",
  "amountA":         "1000000",
  "expectedAmountB": "990000"
}
```

This is idempotent — calling it again with the same `(sourceChainId, intentId)` is a no-op. Once privacy-router delivers, the same key is upserted to `completed` with all three tx hashes. You only ever see one row per intent.

### Option B (server-side, future): progressive publishing

Backend can publish `pending` / `bridging` / `delivered` to the relayer on every privacy-router state transition. FE wouldn't need Option A. Not shipped yet.

For the bridge tx hash *while in-flight*, the privacy router's `GET privacy.tachyon.pe/bridge/<sessionId>` exposes `sweepTxHash` / `bridgeTxHash` / `deliveredTxHash` as they happen. The FE can fall back to it for in-flight detail and treat the relayer as the canonical history.

## Polling Strategy

### Cadence

| Phase | Interval |
|---|---|
| Active poll (just submitted, expecting delivery imminently) | 5–10s |
| Backoff after typical-window passes (see per-chain expectations below) | 15–30s |
| Long tail (waiting on a slow chain like Ethereum mainnet) | 60s |
| **Stop conditions** | `status: completed`, `status: failed`, user navigates away |

A reasonable default: poll every 8s for the first 3 minutes, then every 30s up to the per-chain max. Don't poll forever — if a privacy bridge passes its 25-minute internal deadline it's permanently stuck (and refundable via `/refund/:id`).

### Idempotent retries

The endpoint is read-only; retry transient HTTP/network errors freely with exponential backoff (start 1s, cap 30s).

### Hard ceiling

Privacy router's internal `waitForDelivery` deadlines at 20 minutes. Tachyon settlements typically resolve in <2 minutes. If `pending` for >30 minutes, treat as failed and offer the refund button (privacy bridges) or contact support (tachyon).

## Per-Chain Timing Expectations

Wall-clock from "intent accepted" to `status: completed`. These are the typical bands; outliers happen during chain congestion.

| Source → Dest | Bridge | Typical | Worst | Notes |
|---|---|---|---|---|
| Base ↔ Horizen | Tachyon (solver auction) | 30s–2 min | 5 min | Solver picks up event immediately; auction window is the dominant delay |
| Base ↔ Horizen | Stargate (USDC ↔ USDC.e) | 1–2 min | 5 min | Stargate finalizes on Horizen Caldera quickly |
| {Base, Arb, Op} → MegaETH | LI.FI / Across | 30s–1.5 min | 3 min | Across is the typical tool; ~30s once relayer indexes |
| Ethereum → MegaETH | LI.FI / Relay | 1–3 min | 8 min | Mainnet finality is the bottleneck |
| MegaETH → {Base, Arb, Op} | LI.FI / Across | 30s–2 min | 5 min | Outbound USDT0 → USDC working on Base/Opt; Arb is USDT0→ETH only |
| {Base, Eth, Arb, Op} → Horizen | Stargate (USDC → USDC.e) | 1–3 min | 8 min | LayerZero confirmation rate varies by source chain |
| Horizen → {Base, Eth, Arb, Op} | Stargate (USDC.e → USDC) | 1–3 min | 8 min | Same |
| Base ↔ Horizen | Stargate ZEN OFT | 1–2 min | 5 min | OFT-adapter path |

Privacy bridges add ~30–60s on top of the on-chain timing because of the FireAt jitter (random 20–60s after sweep before dispatching).

## Per-Asset Considerations

| Asset | Decimals | Slippage / fee model | What polling sees |
|---|---|---|---|
| `USDC` (Base/Eth/Arb/Op) | 6 | Bridge-specific (Across ~0.05%, Stargate ~0%) | Standard ERC-20 |
| `USDC.e` (Horizen) | 6 | Stargate USDC ↔ USDC.e is ~0% taker | Same as USDC, just bridged variant |
| `USDT0` (MegaETH) | 6 | LI.FI takes 0.05–0.3% off `toAmount` for stablecoin → USDT0 | "Receive amount" in TransactionRecord may be slightly less than `expectedAmountB` |
| `ETH` (any chain) | 18 | LI.FI handles native sends without ERC-20 approve | Receiving address reads via `eth_getBalance`, not `balanceOf` |
| `ZEN` (Base/Horizen) | 18 | LZ OFT, no spread on transfer; only LZ messaging fee in source-chain native gas (paid by relayer EOA, user sees full amount delivered) | Standard ERC-20 reads |

Slippage doesn't matter for **status detection** — once the destination tx is observed, status flips to `completed` regardless of whether the received amount equaled `expectedAmountB`. The FE can compare `amountA` vs the on-chain delivered amount to surface the realized fee post-hoc.

## Failure Modes

### `status: failed` (privacy bridges only)

The bridge attempt reverted (e.g. Safe execTx GS013/GS026, LI.FI quote-time slippage rejection, Stargate min-amount). Funds are sitting in the TEE Safe.

UX: surface a refund CTA. Call:

```http
POST https://privacy.tachyon.pe/refund/<sessionId>
```

No auth — the sessionID is the bearer. Funds return to the original `userAddress` decrypted from the encrypted payload. Idempotent.

### `pending` for a long time

Three causes, distinguishable by what's in the privacy router state (not relayer):

| Privacy `/bridge/:id` status | Cause | Action |
|---|---|---|
| `pending` (no `sweepTxHash`) | User never sent funds to TEE Safe | Surface "send funds to <TEE Safe address>" reminder |
| `swept`, no progress for >2 min | Dispatcher waiting on FireAt; or batching delay | Wait — should advance shortly. If >5 min, check privacy-router health |
| `bridging`, no `deliveredTxHash` for >10 min | Cross-chain confirmation slow (Ethereum), or stuck | At >25 min the privacy router gives up internally; FE can refund |

For tachyon, `pending` >5 minutes usually means the solver is offline or out of liquidity. Contact ops.

### Network errors during polling

Treat transient HTTP errors as "no update" and retry with backoff. The endpoint is idempotent and read-only.

## Minimal Polling Loop (TypeScript)

```ts
async function pollUntilTerminal(intentId: string, walletSign: () => Promise<{sig: string, ts: number}>) {
  const start = Date.now();
  let interval = 8_000; // start at 8s
  const maxInterval = 30_000;
  const hardDeadline = 30 * 60 * 1000; // 30 minutes

  while (Date.now() - start < hardDeadline) {
    const { sig, ts } = await walletSign();
    const r = await fetch("https://relayer.tachyon.pe/transactions?limit=50", {
      headers: { "X-Signature": sig, "X-Timestamp": String(ts) },
    });
    if (r.ok) {
      const { transactions = [] } = await r.json();
      const row = transactions.find((t: any) => t.intentId === intentId);
      if (row?.status === "completed") return row;
      if (row?.status === "failed")    return row;
    }
    await new Promise(res => setTimeout(res, interval));
    interval = Math.min(maxInterval, Math.floor(interval * 1.2));
  }
  throw new Error("polling timeout");
}
```

Re-sign the wallet every loop iteration; the timestamp must be within 30 minutes of server time. Or sign once and reuse for ~25 minutes before refreshing.

## Quick Reference

```bash
# All transactions for the authenticated wallet
curl -s -H "X-Signature: 0x..." -H "X-Timestamp: <ts>" \
  "https://relayer.tachyon.pe/transactions" | jq

# Filter to in-flight only
curl -s -H "X-Signature: 0x..." -H "X-Timestamp: <ts>" \
  "https://relayer.tachyon.pe/transactions?status=pending" | jq

# Privacy router (no wallet auth — sessionID-bearer)
curl -s "https://privacy.tachyon.pe/bridge/<sessionId>" | jq
```

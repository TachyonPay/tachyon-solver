# Tachyon Frontend Integration

This is the surface a frontend needs to drive Tachyon's three bridge backends end-to-end. Everything below is live on production today.

## Services

| Host | Purpose |
|---|---|
| `https://relayer.tachyon.pe` | Solver registrations, route discovery, transaction history, recipient/stealth flow |
| `https://privacy.tachyon.pe` | Privacy router: encrypted intent submission, status polling, refunds |

The solver runs on the box but the frontend never talks to it directly — all interaction is on-chain (via the Bridge contracts) or through the relayer.

---

## Bridge Types

The user picks one of three backends per bridge. Each is registered with the relayer under a `type` field:

| `type` | Backend | Routes today |
|---|---|---|
| `tachyon` | Existing intent-auction solver, on-chain Bridge contracts | Base ↔ Horizen for USDC ↔ USDC.e and ZEN ↔ ZEN |
| `lifiXtach` | Privacy router → LI.FI / Across | MegaETH ↔ {Base, Eth, Arb, Opt} stables (USDC ↔ USDT0) and dest-side ETH |
| `stargateXtach` | Privacy router → Stargate v2 / LayerZero | Horizen USDC.e ↔ {Base, Eth, Arb, Opt} USDC; Base ↔ Horizen ZEN |

Per-bridge route discovery is identical regardless of type (just change `?type=`).

---

## Discovering Routes

```http
GET https://relayer.tachyon.pe/solvers?type=lifiXtach
```

Response:

```json
{
  "success": true,
  "solvers": [
    {
      "type": "lifiXtach",
      "address": "0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C",
      "routes": [
        { "sourceChain": "8453", "destChain": "4326", "tokenA": "USDC", "tokenB": "USDT0" },
        { "sourceChain": "8453", "destChain": "4326", "tokenA": "USDC", "tokenB": "ETH" },
        ...
      ],
      "tokens": [
        { "chainId": "8453", "symbol": "USDC", "address": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", "decimals": 6 },
        { "chainId": "4326", "symbol": "USDT0", "address": "0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb", "decimals": 6 },
        { "chainId": "4326", "symbol": "ETH", "address": "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE", "decimals": 18 },
        ...
      ],
      "registeredAt": "...",
      "lastSeen": "..."
    }
  ]
}
```

- `routes[]` — pairs the user can pick. `sourceChain` / `destChain` are numeric chain IDs as strings.
- `tokens[]` — sidecar with everything needed to render: contract `address` per `(chainId, symbol)` plus `decimals`. ETH uses the LI.FI native sentinel `0xEee…EEeE` — render as native ETH and skip any ERC-20 approve.
- `address` is a stable identifier for the bridge (TEE Safe for privacy router, EOA for tachyon).

To merge for a unified picker:

```js
const types = ["tachyon", "lifiXtach", "stargateXtach"];
const all = await Promise.all(types.map(t => fetch(`/solvers?type=${t}`).then(r => r.json())));
```

`GET /get-routes?type={type}&sourceChain=...&destChain=...` returns the same data filtered.

---

## Chain Configs

| chainID | Name | Native | Public RPC | LZ v2 EID | Block explorer |
|---|---|---|---|---|---|
| 1 | Ethereum | ETH | `https://eth-mainnet.g.alchemy.com/v2/<key>` | 30101 | etherscan.io |
| 10 | Optimism | ETH | `https://opt-mainnet.g.alchemy.com/v2/<key>` | 30111 | optimistic.etherscan.io |
| 8453 | Base | ETH | `https://base-mainnet.g.alchemy.com/v2/<key>` | 30184 | basescan.org |
| 42161 | Arbitrum | ETH | `https://arb-mainnet.g.alchemy.com/v2/<key>` | 30110 | arbiscan.io |
| 4326 | MegaETH | ETH | `https://mainnet.megaeth.com/rpc` | 30398 | megaexplorer.xyz |
| 26514 | Horizen | ZEN | `https://horizen.calderachain.xyz/http` | 30399 | horizen.calderaexplorer.xyz |

LZ EIDs are needed only if the FE wants to show "via LayerZero" or query LZ scan directly.

---

## TEE Safe Addresses (privacy router)

The address the user sends source tokens to when using the privacy router. Lookup is per source chain:

| chainID | TEE Safe |
|---|---|
| 1 (Ethereum) | `0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C` |
| 10 (Optimism) | `0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C` |
| 42161 (Arbitrum) | `0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C` |
| 4326 (MegaETH) | `0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C` |
| 8453 (Base) | `0xa4A8E2dDA54321E257615fef9FF467401D1C17D4` |
| 26514 (Horizen) | `0x71ca2818D92123dfC7072486F73aDBF5F1E28974` |

The frontend should **not** ask the user to send funds anywhere else. Same Safes will continue to be used until rotated. If we rotate, this list updates and the FE can also read it from the privacy router's registration `address` field on `/solvers?type=lifiXtach`.

Base + Horizen TEE Safes are deliberately the same address as the existing tachyon-solver liquidity Safes (commingled for v0). The other four chains have a fresh dedicated address.

---

## Auth Models

Three different auth styles across the API surface:

1. **Wallet signature** — for routes that act on a user's history. The user signs `tachyon-auth:{unix_timestamp}` with their wallet; FE sends the recovered hex sig + timestamp:
   ```
   X-Signature: 0x<sig>
   X-Timestamp: 1714680000
   ```
   Used by `POST /transactions`, `GET /transactions`. 30-min validity.

2. **sessionID-bearer** — for the privacy router. Whoever holds the sessionID returned by `POST /bridge` can read status and trigger refunds. Refunds always go to the original `userAddress` decrypted from the encrypted payload, so a leaked sessionID can only do the legitimate user a favor.

3. **Admin API key** — never exposed to the FE. Internal only.

---

## Tachyon Flow (intent auction)

1. **Discover route**: `GET /solvers?type=tachyon`. Response gives the supported tokens per chain (USDC ↔ USDC.e, ZEN ↔ ZEN between Base 8453 and Horizen 26514).

2. **Approve token to the Bridge contract** on source chain.

   Bridge contract addresses (from existing tachyon deployment):
   - Base (8453): `0x52F77B624a9f20d41a60eF550e5792a00747262c`
   - Horizen (26514): `0x1809703DE921177cca0f1c80FB58D788E09AdDB4`

3. **Pre-create a pending transaction record** so it shows up in history immediately:
   ```http
   POST https://relayer.tachyon.pe/transactions
   X-Signature: ...
   X-Timestamp: ...
   {
     "intentId": "<intent id you'll target — currentLatest+1>",
     "userAddress": "<user wallet>",
     "sourceChainId": "8453",
     "destChainId": "26514",
     "tokenA": "0x833589f...",
     "tokenB": "0xDF7108f8...",
     "amountA": "1000000",
     "expectedAmountB": "950000"
   }
   ```

4. **Generate stealth recipients** (one or more) on the relayer side, then store them so the solver can read them when it settles:
   ```http
   POST https://relayer.tachyon.pe/recipient/generate-stealth
   { "intentId": "<id>", "count": 3 }
   ```
   Returns `{ stealthAddress, safeAddress }` per recipient. Use the `safeAddress` values.

   ```http
   POST https://relayer.tachyon.pe/store-recipients
   {
     "intentId": "<id>",
     "sourceChainId": 8453,
     "recipients": ["0xSafe1", "0xSafe2", "0xSafe3"],
     "amounts":    ["300000", "350000", "300000"]
   }
   ```
   Sum of `amounts` MUST equal `expectedAmountB` exactly or the solver's settle will revert.

5. **Call `Bridge.createIntent(...)`** on the source chain with the user's wallet. The intent ID is returned by the call.

6. **Solver bids + settles automatically** (off-chain). Typically completes within ~30s plus the auction duration (currently 100s on prod; we can shorten).

7. **Status**: query `GET /transactions` (wallet-auth) to see the record transition from `pending` → `completed`. Or read the Bridge contract directly: `Bridge.intents(intentId)`.

8. **Stealth recipients claim funds**:
   ```http
   GET  /recipient/addresses?intentId=<id>
   POST /recipient/derive-stealth-key  { intentId, recipientIndex }
   POST /recipient/deploy-safe         { intentId, recipientIndex }
   ```

---

## Privacy Router Flow

Identical regardless of whether the route is `lifiXtach` or `stargateXtach` — the router picks the rail based on the route automatically.

### 1. Get the service's ECIES public key

```http
GET https://privacy.tachyon.pe/pubkey
→ { "success": true, "publicKey": "0x047f46a8..." }
```

Cache it; rotates infrequently.

### 2. Predict the destination stealth Safe

The user's funds will arrive at a CREATE2-predicted Safe address on the destination chain that is owned by a fresh stealth EOA they generate. The Safe doesn't need to be deployed before the bridge — it can be deployed later when the user wants to spend the funds.

Predict the address with the canonical Safe v1.3.0 addresses:
- ProxyFactory: `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2`
- Singleton: `0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552`
- FallbackHandler: `0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4`

CREATE2 inputs:
- `setupData = singleton.setup([stealthOwner], 1, 0, 0x, fallbackHandler, 0, 0, 0)`
- `saltNonce = keccak256(stealthOwnerAddress)` (or any value the FE wants to fix)
- `address = keccak256(0xff ++ factory ++ keccak256(keccak256(setupData) ++ saltNonce) ++ keccak256(initCode))[12:]`

Same factory + singleton on every chain we support, so the predicted address is identical across chains for the same stealth owner.

### 3. Build the intent payload, encrypt, submit

#### Encryption details

| Property | Value |
|---|---|
| Algorithm | ECIES |
| Curve | secp256k1 |
| KDF | HKDF-SHA256 |
| Cipher | AES-256-GCM |
| Server library | `github.com/ecies/go/v2` |
| FE library (recommended) | `eciesjs` (npm) — same wire format |
| Public key format | uncompressed SEC1 → `0x04 ‖ X(32) ‖ Y(32)` (65 bytes / 132 hex chars including the 0x04 prefix) |
| Plaintext | UTF-8 JSON of the payload below; field order doesn't matter |
| Wire encoding | hex string, optional `0x` prefix, sent as `{ "encrypted": "<hex>" }` |

The pubkey from `GET /pubkey` is already in the right format:

```json
{ "success": true, "publicKey": "0x04abc1...d3e4" }
```

`eciesjs` accepts that hex (after stripping `0x`) directly.

#### TypeScript reference (FE-side)

```bash
npm i eciesjs
```

```ts
import { encrypt } from "eciesjs";

// 1. Fetch + cache the service pubkey
const { publicKey } = await fetch("https://privacy.tachyon.pe/pubkey").then(r => r.json());
const pubKeyBytes = Buffer.from(publicKey.replace(/^0x/, ""), "hex"); // 65 bytes

// 2. Build the payload — every field mandatory, camelCase, strings (not numbers) for chainIds + amount
const payload = {
  sessionId:       "privacy-" + crypto.randomUUID(),
  userAddress:     userWallet,
  sourceChainId:   "8453",
  sourceToken:     "USDC",
  inboundSafe:     userWallet,                       // unused in v0 — pass userWallet
  amount:          "1000000",                        // atomic units as STRING (avoid JS number precision)
  destChainId:     "4326",
  destToken:       "USDT0",
  destStealthSafe: predictedSafeAddress,             // CREATE2 prediction from step 2
  unixTimestamp:   Math.floor(Date.now() / 1000),    // server rejects > 24h skew
};

// 3. Encrypt + hex-encode + POST
const plaintext = Buffer.from(JSON.stringify(payload), "utf8");
const cipher = encrypt(pubKeyBytes, plaintext);
const encryptedHex = "0x" + cipher.toString("hex");

const submitResp = await fetch("https://privacy.tachyon.pe/bridge", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ encrypted: encryptedHex }),
}).then(r => r.json());
// → { success: true, sessionId: "privacy-..." }

const sessionId = submitResp.sessionId;
```

In a Vite / browser bundler, import `Buffer` from the `buffer/` polyfill if `globalThis.Buffer` isn't injected:

```ts
import { Buffer } from "buffer";
```

Token symbols accepted: `USDC`, `USDT0`, `USDC.e`, `ETH`, `ZEN` (case-insensitive). Server validates the route at submit time and rejects unsupported pairs with `route XXX not supported`.

#### What the server does on receipt

1. Strips optional `0x` prefix
2. Hex-decodes the blob
3. ECIES-decrypts with its private key
4. JSON-unmarshals into `BridgeIntentPayload`
5. Validates: address checksums, chain pair, supported route, timestamp skew (≤ 24h), positive amount
6. Persists with `status: "pending"` + returns `sessionId`

If any step fails → HTTP 400 + `{ success: false, error: "<reason>" }`, nothing persisted.

Common errors:

| Server error | Cause |
|---|---|
| `route 8453:USDC → 26514:UNKNOWN not supported` | Unknown token symbol or chain pair not in the route table |
| `unixTimestamp ... skewed beyond 24h0m0s` | Client clock drift |
| `userAddress "0x..." invalid` | Bad checksum / wrong length |
| `amount "..." is not a positive integer` | Passed a number / decimal / zero |
| `sessionId already submitted` (HTTP 409) | Re-using a sessionId from a previous attempt — generate a fresh one |

#### Common pitfalls

- **Use uncompressed pubkey.** The `/pubkey` response is 65 bytes (`0x04…`); pass that whole buffer to `encrypt()`. Don't compress to 33 bytes.
- **Strings, not BigInts**, for `amount` and chain IDs. `JSON.stringify(BigInt(...))` throws.
- **Encrypt the JSON string, not an object.** Pass `Buffer.from(JSON.stringify(payload), "utf8")` to `encrypt()`.
- **Don't reuse sessionIds.** A re-submit of an old sessionId returns 409. Generate fresh on retry.
- **Don't log the cipher.** It contains `destStealthSafe` and `amount` — the entire reason for encrypting.

`sessionId` is the bearer for status reads and refunds. Persist it locally (localStorage / IndexedDB).

#### Go reference

If preferred over JS, the existing test [`tachyon-prod/test/e2e/privacy_flow/main.go`](test/e2e/privacy_flow/main.go) does the full flow end-to-end: pubkey fetch, payload build, ECIES encrypt via `github.com/ecies/go/v2`, POST `/bridge`, source-token transfer, status poll, dest-balance verify. Same wire format as the JS snippet above.

### 4. Send source funds to the TEE Safe

The user transfers `amount` of `sourceToken` from their EOA to the per-chain TEE Safe (table above). Standard ERC-20 transfer or native send.

### 5. Poll status

```http
GET https://privacy.tachyon.pe/bridge/<sessionId>
```

Response:

```json
{
  "success": true,
  "sessionId": "privacy-test-...",
  "status": "delivered",
  "sourceChainId": "8453",
  "destChainId": "26514",
  "created": "2026-05-02T17:32:10Z",
  "updated": "2026-05-02T17:35:01Z",
  "sweepTxHash": "0xaa3234c3...",
  "bridgeTxHash": "0x5a99f3...",
  "deliveredTxHash": "0x5767c7...",
  "refundTxHash": "",
  "refundable": false,
  "error": ""
}
```

Status lifecycle:

| status | meaning |
|---|---|
| `pending` | intent received, waiting for source-side deposit |
| `swept` | deposit detected at TEE Safe; queued for dispatch |
| `bridging` | bridge tx submitted; waiting for cross-chain confirmation |
| `delivered` | confirmed on dest chain — funds in `destStealthSafe` |
| `failed` | unrecoverable error (FE should show refund button) |
| `refunded` | funds returned to `userAddress` |

`refundable: true` iff the FE should show a refund CTA (status is `swept` or `failed`).

Suggested poll cadence: 8s while in-flight, stop on `delivered` / `failed` / `refunded`.

### 6. Refund (when needed)

```http
POST https://privacy.tachyon.pe/refund/<sessionId>
```

No auth — sessionID is the bearer. The refund always goes to the `userAddress` in the original encrypted payload.

Response:

```json
{
  "success": true,
  "sessionId": "privacy-test-...",
  "refundTxHash": "0x9c9d21ee...",
  "recipient": "0xC3A25467...",
  "sourceChain": "8453",
  "amount": "1000000"
}
```

If the intent is already `refunded`, returns `{ success: true, refundTxHash, message: "already refunded" }` (idempotent). If status is `pending`, `bridging`, or `delivered`, returns 409 with `not refundable`.

---

## Transaction History

Unified across both bridge types — the privacy router publishes `delivered` bridges to the relayer's transaction store, so this single endpoint covers everything for a given wallet.

Auth: wallet signature (X-Signature, X-Timestamp).

```http
GET https://relayer.tachyon.pe/transactions?status=completed&page=1&limit=20
X-Signature: ...
X-Timestamp: ...
```

Response:

```json
{
  "success": true,
  "transactions": [
    {
      "intentId": "privacy-test-95ba1b70-...",
      "userAddress": "0xC3A2...",
      "sourceChainId": "8453",
      "destChainId": "26514",
      "tokenA": "USDC",
      "tokenB": "USDC.E",
      "amountA": "1000000",
      "expectedAmountB": "",
      "status": "completed",
      "depositTxHash": "0xaa3234c3...",
      "solveTxHash":   "0x5a99f3...",
      "settleTxHash":  "0x5767c7...",
      "createdAt": "...",
      "updatedAt": "...",
      "completedAt": "..."
    },
    {
      "intentId": "42",
      "userAddress": "0xC3A2...",
      "sourceChainId": "8453",
      "destChainId": "26514",
      "tokenA": "0x833589f...",
      "tokenB": "0xDF7108f8...",
      "amountA": "1000000",
      "expectedAmountB": "950000",
      "status": "completed",
      ...
    }
  ],
  "page": 1,
  "limit": 20,
  "total": 2,
  "totalPages": 1
}
```

Distinguishing the two types in the FE:

- `intentId` is a 32-byte hex string (`privacy-test-…`) for privacy bridges; an integer for tachyon
- `tokenA` / `tokenB` are symbols (`USDC`, `USDT0`, `USDC.E`) for privacy; ERC-20 contract addresses for tachyon
- `solveTxHash` / `settleTxHash` are populated for both, but for privacy bridges they correspond to different stages (see the privacy table below)

| Privacy lifecycle | TransactionRecord field |
|---|---|
| sweep (user → TEE Safe) | `depositTxHash` |
| bridge (TEE Safe → bridge call) | `solveTxHash` |
| delivery (dest chain receipt) | `settleTxHash` |

---

## Endpoint Cheat Sheet

| Service | Method | Path | Auth | Purpose |
|---|---|---|---|---|
| relayer | GET | `/health` | none | Liveness |
| relayer | GET | `/solvers?type=` | none | Per-bridge catalog with tokens sidecar |
| relayer | GET | `/get-routes?type=&sourceChain=&destChain=&tokenA=&tokenB=` | none | Filtered route discovery |
| relayer | POST | `/transactions` | wallet | Pre-record a pending intent |
| relayer | GET | `/transactions?status=&page=&limit=` | wallet | Unified history |
| relayer | GET | `/intent-details/{intentId}` | none | Settlement details for tachyon intent |
| relayer | POST | `/recipient/generate-stealth` | none | Get stealth address + predicted Safe per recipient |
| relayer | POST | `/store-recipients` | none | Register stealth recipients for a tachyon intent |
| relayer | POST | `/recipient/derive-stealth-key` | wallet | Recover the stealth EOA's privkey |
| relayer | POST | `/recipient/deploy-safe` | wallet | Deploy a Safe on dest chain at the predicted address |
| privacy | GET | `/health` | none | Liveness |
| privacy | GET | `/pubkey` | none | ECIES public key for encryption |
| privacy | POST | `/bridge` | none (encrypted body) | Submit encrypted intent → sessionId |
| privacy | GET | `/bridge/{sessionId}` | sessionID-bearer | Status + tx hashes + refundable flag |
| privacy | POST | `/refund/{sessionId}` | sessionID-bearer | Refund a swept/failed intent to the original EOA |

---

## Reference Tests (read these for the canonical flow)

Two e2e programs in `tachyon-prod/test/e2e/` exercise every endpoint above against live mainnet. They're the source of truth for payload shapes and call sequencing — when porting to the FE, mirror them.

| Path | What it covers |
|---|---|
| [`test/e2e/privacy_flow/main.go`](test/e2e/privacy_flow/main.go) | Privacy router end-to-end: GET `/pubkey`, build payload, ECIES encrypt, POST `/bridge`, send source token to TEE Safe, poll `/bridge/:id`, read dest balance at the predicted CREATE2 stealth Safe. Parameterized via `PRIVACY_TEST_DEST_CHAIN` + `PRIVACY_TEST_DEST_TOKEN` so the same test drives both `lifiXtach` and `stargateXtach`. |
| [`test/e2e/complete_flow.go`](test/e2e/complete_flow.go) | Tachyon end-to-end: token approve to Bridge, pre-record pending transaction (wallet sig), generate stealth recipients, store recipients (with the sum-equals-AmountB constraint), `Bridge.createIntent`, wait for solver settlement, poll `/transactions`, derive stealth key + deploy Safe + verify balance. |
| [`test/e2e/three_bridges/run.sh`](test/e2e/three_bridges/run.sh) | Driver that runs all three flows (`tachyon`, `lifiXtach`, `stargateXtach`) sequentially with sensible defaults. |

**Gap to be aware of:** none of the tests exercise `POST /refund/:id` yet. Manual curl verification is what we ran in production, but a refund branch should be added to `privacy_flow/main.go` (e.g. opt-in via `PRIVACY_TEST_REFUND=1`) — TODO.

The encryption in `privacy_flow/main.go` uses Go's `github.com/ecies/go/v2`; the JS `eciesjs` package produces an identical wire format, so the TypeScript snippet above and the Go test are interchangeable references.

---

## Operational Notes for the FE

- **Same predicted Safe address on every chain** for a given stealth owner — the canonical Safe v1.3.0 factory + initializer + saltNonce is consistent across all 6 chains.
- **TEE Safes hold no native gas** — they don't need to. The relayer EOA forwards `msg.value` for LayerZero messaging fees on Stargate.
- **Decimals**: USDC / USDT0 / USDC.e are 6; ETH / ZEN are 18.
- **Stargate vs LI.FI auto-routing**: the privacy router picks the rail per route. FE doesn't need to specify — just pass the right `sourceChainId`, `destChainId`, `sourceToken`, `destToken` and the router routes it. Use `?type=lifiXtach` vs `?type=stargateXtach` only for the route picker UI, not for submission.
- **Slippage**: LI.FI quotes already account for it (~0.6% for Across). Stargate USDC ↔ USDC.e is 1:1 with no token-side slippage; LZ messaging fee is paid in source-chain native by the relayer.
- **Quote staleness**: the privacy router fetches the LI.FI quote at dispatch time (after sweep + FireAt timer), not at submit time, so a stale quote isn't an issue. For the FE, no need to attach a quote.
- **MegaETH outbound** is currently published but won't reach `delivered` until we ship a separate balance-diff watcher on MegaETH source — at the moment, MegaETH-source intents are gated off in `IsLifiRoute` (the watcher only sees ERC-20 Transfer logs and MegaETH ETH is native). FE should not surface MegaETH USDT0/USDC outbound until that's lifted.

---

## Quick Sanity Curls

```bash
# Health
curl -s https://relayer.tachyon.pe/health | jq
curl -s https://privacy.tachyon.pe/health | jq

# All bridge types at a glance
for t in tachyon lifiXtach stargateXtach; do
  echo "== $t =="
  curl -s "https://relayer.tachyon.pe/solvers?type=$t" | jq -c '{routes:([.solvers[].routes|length]|add), tokens:([.solvers[].tokens//[]|length]|add)}'
done

# Privacy router pubkey
curl -s https://privacy.tachyon.pe/pubkey | jq -r .publicKey

# Status of a real session
curl -s https://privacy.tachyon.pe/bridge/<sessionId> | jq

# Refund a swept/failed session
curl -s -X POST https://privacy.tachyon.pe/refund/<sessionId> | jq
```

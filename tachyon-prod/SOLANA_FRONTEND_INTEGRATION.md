# Solana Frontend Integration Guide

This is the spec the frontend team needs to add Solana support to the
Tachyon bridge UI. Backend is feature-complete; nothing in this doc
requires further backend work unless explicitly noted.

The existing `FRONTEND_INTEGRATION.md` covers the EVM flow. This doc
adds the Solana parts and explains where they slot into the existing
flows.

---

## 1. Architectural overview

Solana support is **bidirectional**:

| Source | Destination | Source-side contract | Destination-side contract |
|---|---|---|---|
| EVM (Horizen / Base) | EVM | `BridgeIntent.sol :: createIntent` | `BridgeIntent.sol :: solveIntentOnChain2` |
| **EVM** | **Solana** | `BridgeIntent.sol :: createIntentToSolana` | Anchor `bridge_intent :: solve_intent_on_chain2` |
| **Solana** | EVM | Anchor `bridge_intent :: create_intent` | `BridgeIntent.sol :: solveIntentOnChain2` |
| **Solana** | Solana | Anchor `bridge_intent :: create_intent` | Anchor `bridge_intent :: solve_intent_on_chain2` |

The user-facing flows are:

1. **Create intent** (user-signed)
2. **Wait for solver delivery** (background polling)
3. **Settle** (relayer-driven, FE just polls status)
4. **Stealth address registration + sweep** (optional privacy add-on, user-signed)

The wallet the user connects is determined by the **source** chain. EVM
source → MetaMask/WalletConnect/etc. Solana source → Phantom / Solflare /
Backpack via the standard Solana Wallet Adapter.

---

## 2. Packages the FE needs to add

Per the `solana-dev` skill recommendations:

```
# preferred (framework-kit + Kit)
@solana/client                    # framework-kit RPC client
@solana/react-hooks               # framework-kit hooks
@solana/kit                       # transaction message + codecs
@solana/wallet-adapter-react      # wallet connect
@solana/wallet-adapter-react-ui   # default modal
@solana/wallet-adapter-wallets    # Phantom / Solflare / Backpack adapters

# Anchor IDL client + Borsh helpers
@coral-xyz/anchor                 # IDL → typed client (the bridge_intent IDL is shipped)

# stealth crypto
tweetnacl                         # nacl.box.open for sealed salts
ed2curve                          # ed25519 sk → x25519 sk conversion (or a 10-LOC inline impl)
```

If a legacy library forces `@solana/web3.js`, isolate it behind
`@solana/web3-compat` per the skill — don't let web3.js types leak across
the rest of the app.

The Anchor IDL the FE consumes is published at:

```
solana/idl/bridge_intent.json
```

— stable path, regenerated only on program changes. Generate the typed
client with:

```bash
anchor idl init --filepath solana/idl/bridge_intent.json <programId>
# or for codegen:
codama generate --idl solana/idl/bridge_intent.json --output src/anchor-client
```

---

## 3. Bootstrap: load program info

On app boot, fetch program info once and cache:

```http
GET /solana/program-info
```

Response:

```jsonc
{
  "chains": {
    "30168": {
      "name": "solana-mainnet",
      "chainId": "30168",
      "programId": "64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk",
      "usdcMint": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
      "wsolMint": "So11111111111111111111111111111111111111112",
      "commitment": "confirmed",
      "explorer": "https://explorer.solana.com",
      "configPda": "...",
      "escrowAuthorityPda": "...",
      "seeds": {
        "config":  "636f6e666967",
        "escrow":  "657363726f77",
        "intent":  "696e74656e74",
        "bids":    "62696473",
        "solved":  "736f6c766564",
        "stealth": "737465616c7468",
        "relayer": "72656c61796572"
      }
    },
    "40168": { ... devnet ... }
  }
}
```

The `seeds` map is the source of truth for PDA derivation. Use it to
build PDAs locally rather than hardcoding the strings.

---

## 4. Address & chain ID conventions

- **EVM addresses**: `0x` prefix, 20 bytes hex (40 chars after prefix).
- **Solana pubkeys**: base58, 32 bytes (44 chars).
- **Chain IDs**:
  - EVM: standard EIP-155 (e.g. Base = 8453, Horizen mainnet = 26514).
  - **Solana: LayerZero EID** — 30168 mainnet, 40168 devnet/testnet. Treat
    it like any other "chain ID" in the existing UI; the contract's
    `destChainId` field already accepts it.
- **`recipient` field on Solana destinations**: 32 bytes. Either:
  - A raw Solana pubkey (the user's wallet) — base58.
  - A stealth PDA — also base58, derived per §8.

In transit (HTTP responses): the `/solana/intent/:chainId/:intentId`
endpoint returns recipient as either base58 OR `0x`-hex. We pad EVM
addresses to 32 bytes with leading zeros on-chain; the GET handler
detects this padding and renders them back as `0x...20-bytes` for FE
ease. See [pkg/api/relayer_solana_reads.go](pkg/api/relayer_solana_reads.go).

---

## 5. Flow A — EVM → Solana intent

User on Horizen/Base wants to send USDC to a Solana wallet.

### 5a. Create intent (EVM-side)

Call the **new** function on `BridgeIntent.sol`:

```solidity
createIntentToSolana(
    address tokenA,         // ERC-20 on EVM
    bytes32 tokenBMint,     // SPL mint on Solana, 32 bytes
    uint256 amountA,
    uint256 expectedAmountB, // in tokenB decimals (USDC = 6, WSOL = 9)
    uint256 reward,
    uint64  destSolanaEID,   // 30168 or 40168
    bytes32 recipient,       // Solana pubkey OR stealth PDA, 32 bytes
    uint256 auctionDuration  // 5..86400 seconds
)
```

ABI for this function ships in [tachyon-prod/contracts/BridgeIntent.json](tachyon-prod/contracts/BridgeIntent.json)
— same file the EVM solver loads, so the FE wagmi/ethers config is
unchanged. `IntentCreatedToSolana` event is emitted; track its
`intentId` for status polling.

### 5b. Convert recipient pubkey → bytes32

```ts
import { PublicKey } from "@solana/web3.js"; // or kit equivalent
const pk = new PublicKey(recipientBase58);
const recipient32: `0x${string}` = `0x${Buffer.from(pk.toBytes()).toString("hex")}`;
```

### 5c. Convert SPL mint → bytes32

Same as above for `tokenBMint`. The bridge enforces the mint is in the
program's allowlist (USDC + WSOL). Querying allowed mints:

```http
GET /solana/program-info/30168
```

returns `usdcMint` + `wsolMint` to render in the dropdown.

### 5d. Status polling

Cross-chain solve status:

```http
GET /solana/solved/<srcChainId>/<srcIntentId>
```

Returns:

```json
{ "30168": false, "40168": true }
```

Map keyed by Solana chain ID. `true` means `solve_intent_on_chain2` has
completed on that cluster — funds delivered. The settle step on the
source chain follows automatically; the existing EVM `IntentSettled`
event signals user-visible completion.

---

## 6. Flow B — Solana → EVM intent

User on Solana wants to send USDC to an EVM wallet.

### 6a. Build the transaction client-side

Using the Anchor IDL + the user's Solana wallet:

```ts
const program = new anchor.Program(idl, new anchor.AnchorProvider(connection, wallet, {}));
const intentId = (await program.account.config.fetch(configPda)).nextIntentId;

await program.methods
  .createIntent(
    new BN(amountA),               // u64 in tokenA decimals
    new BN(expectedAmountB),       // u64 in tokenB decimals (EVM dest decimals)
    new BN(reward),
    new BN(destEvmChainId),        // e.g. 8453 for Base
    Array.from(recipient32),       // [u8;32] — see §6b
    new BN(auctionDurationSeconds)
  )
  .accounts({
    user: wallet.publicKey,
    config: configPda,
    tokenAMint,
    tokenBMint,                    // sentinel: pass tokenA again for non-Solana dest (see backend note)
    userTokenAAta,
    escrowAuthority: escrowAuthorityPda,
    escrowTokenAAta,               // ATA(escrowAuthorityPda, tokenAMint)
    intentRecord: intentPda,       // [seeds.intent, intentIdLE]
    intentBids: bidsPda,           // [seeds.bids, intentIdLE]
    tokenProgram: TOKEN_PROGRAM_ID,
    associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
    systemProgram: SystemProgram.programId,
    rent: SYSVAR_RENT_PUBKEY,
  })
  .rpc();
```

### 6b. EVM recipient as bytes32

For Solana → EVM, the `recipient` field on the Anchor side is `[u8; 32]`.
EVM addresses are 20 bytes; we left-pad with 12 zero bytes:

```ts
const recipient32 = new Uint8Array(32);
recipient32.set(hexToBytes(evmAddrLowercase), 12); // bytes 12..32 = 20-byte addr
```

The Solana solver reads this PDA and submits `solveIntentOnChain2` on
the destination EVM contract with the unpadded address.

### 6c. Status polling

EVM-destination solve completion is reported by the existing
`IntentSolvedOnChain2` event on the EVM contract. Use the same EVM-side
polling path the FE already has.

To read the **source-side** Solana intent state (for "auction in
progress" / "your intent" UX):

```http
GET /solana/intent/<chainId>/<intentId>
```

Response:

```jsonc
{
  "intentId": "1",
  "user": "<base58>",
  "tokenAMint": "<base58>",
  "tokenBMint": "<base58>",
  "amountA": "1000000",
  "expectedAmountB": "950000",
  "reward": "50000",
  "destChainId": "8453",
  "recipient": "0xabc...",        // EVM addr rendered as 0x-hex (auto-detected from left-pad)
  "endTime": 1736120399,
  "winningSolver": "11111111111111111111111111111111", // default = no winner yet
  "winningBid": "0",
  "completed": false,
  "deposited": false
}
```

`u64` fields are returned as base-10 strings to avoid JSON precision loss.

---

## 7. Flow C — Solana → Solana intent

Same as Flow B, but `destChainId` is also a Solana EID (30168/40168).
The Anchor program handles both source and destination roles; FE work
is identical to §6 except the `recipient` is base58 (no left-pad).

---

## 8. Stealth addresses

Two patterns. Both supported.

### 8a. Self-managed (FE generates salt)

Best when you want guaranteed privacy without trusting the relayer.

```ts
import { randomBytes } from "tweetnacl";

const ephemeralSalt = randomBytes(32);                          // 32 bytes
const [stealthPda] = PublicKey.findProgramAddressSync(
  [Buffer.from("stealth"), wallet.publicKey.toBuffer(), ephemeralSalt],
  programId
);

// Optionally store the salt encrypted under a passphrase / wallet-derived key.

await program.methods
  .registerStealth(Array.from(ephemeralSalt))
  .accounts({
    user: wallet.publicKey,
    stealthRegistration: stealthPda,
    systemProgram: SystemProgram.programId,
  })
  .rpc();
```

The `stealthPda` becomes the recipient in any subsequent intent. The
relayer is **not involved**; the salt is the user's secret.

### 8b. Relayer-managed (relayer issues sealed salt)

Best for FEs that don't want to manage long-term secret storage.

#### Step 1: ask the relayer for a sealed salt

```http
POST /solana/recipient/register-stealth
Content-Type: application/json

{
  "userPubkey": "<base58 Solana wallet>",
  "programId":  "<base58 bridge_intent>"
}
```

Response:

```jsonc
{
  "stealthPda": "<base58>",
  "bump": 252,
  "issuedAt": 1736120000,
  "sealed": {
    "ephemeralX25519Pub": "<32 bytes hex>",
    "nonce":              "<24 bytes hex>",
    "ciphertext":         "<48 bytes hex>"   // 32-byte salt + 16-byte Poly1305 tag
  }
}
```

#### Step 2: open the sealed salt in-browser

```ts
import nacl from "tweetnacl";

// Convert the user's ed25519 secret key (from wallet provider) to X25519.
// Most wallets don't expose the raw secret. Two common approaches:
//   (a) the user signs a fixed message; derive a Curve25519 key from sig.
//   (b) use a wallet that exposes ed25519→X25519 directly.
// Recommended: the user signs a canonical message ONCE per session and we
// cache the derived X25519 sk in memory for that session.

const userX25519Sk = ed25519SkToX25519Sk(userEd25519Sk);          // 32 bytes
const ephPub = hexToBytes(sealed.ephemeralX25519Pub);
const nonce = hexToBytes(sealed.nonce);
const ct = hexToBytes(sealed.ciphertext);

const salt = nacl.box.open(ct, nonce, ephPub, userX25519Sk);      // 32 bytes
if (!salt) throw new Error("salt decryption failed");
```

`ed25519SkToX25519Sk` is a well-known recipe (sha512 the seed, take the
first 32 bytes, clamp). The `ed2curve` npm package or a 10-LOC inline
impl works.

#### Step 3: register on-chain with the decrypted salt

Same `registerStealth` call as 8a. The relayer **doesn't** submit this
tx — the user does, with their wallet.

### 8c. Sweep stealth funds

When funds have arrived at the stealth PDA's ATA, the user sweeps:

```ts
const stealthAta = getAssociatedTokenAddressSync(mint, stealthPda, true);
const userAta = getAssociatedTokenAddressSync(mint, wallet.publicKey, false);

await program.methods
  .sweepStealth(Array.from(ephemeralSalt))
  .accounts({
    user: wallet.publicKey,
    stealthRegistration: stealthPda,
    mint,
    stealthAta,
    userAta,
    tokenProgram: TOKEN_PROGRAM_ID,
    associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
  })
  .rpc();
```

The Anchor program enforces `signer == registered.user` — only the
original user can sweep.

### 8d. PDA derivation reference

For a quick sanity check before submitting:

```http
POST /solana/recipient/derive-stealth-pda
Content-Type: application/json

{
  "userPubkey":    "<base58>",
  "ephemeralSalt": "<32 bytes hex>",
  "programId":     "<base58>"
}
```

Returns `{ "stealthPda": "...", "bump": ... }`. Pure derivation; no
side effects.

---

## 9. Token / amount handling

- **USDC**: 6 decimals. Same on EVM and Solana.
- **WSOL**: 9 decimals. Solana-side only. Wrapped SOL — recipient can
  unwrap to native SOL with one `closeAccount` instruction; the FE
  may surface this as a "Receive native SOL?" toggle.
- **Token-2022 mints are explicitly rejected** by the Anchor program's
  allowlist (only classic SPL Token mints are accepted). See §11.
- **Decimals lookup**: query the mint with `getMint` (Solana RPC) and
  cache. Standard practice.

The price oracle on the backend already supports SOL and WSOL — both
alias to the same CoinGecko/CoinCap/Binance price feeds. The FE doesn't
need to do any price math; the bid/profitability checks happen
server-side.

---

## 10. Status polling matrix

| Direction | Source-side status | Destination-side status |
|---|---|---|
| EVM→EVM | EVM logs (existing) | EVM logs (existing) |
| **EVM→Solana** | EVM `IntentCreatedToSolana`, `IntentSettled` | `GET /solana/solved/:srcChainId/:srcIntentId` |
| **Solana→EVM** | `GET /solana/intent/:chainId/:intentId` | EVM `IntentSolvedOnChain2` (existing) |
| **Solana→Solana** | `GET /solana/intent/:chainId/:intentId` | `GET /solana/solved/:srcChainId/:srcIntentId` |

For the source-side Solana intent state, you can ALSO subscribe to
program logs directly via Helius enhanced webhooks if you don't want to
poll. Backend exposes `/solana/helius-webhook` as the relayer-side
ingestion endpoint, not a FE concern.

---

## 11. Security notes the FE should be aware of

- **Token-2022 rejection.** The Anchor program uses
  `anchor_spl::token::Mint` (not `token_interface`), which validates
  the mint is owned by the classic SPL Token program. Token-2022 mints
  fail constraint checks. If a user tries to bridge a Token-2022 mint
  the source-side `create_intent` reverts with `AccountOwnerMismatch`.
  Surface this clearly in the UI (don't let the user select Token-2022
  mints in the picker).
- **Recipient validation.** The program rejects `[0u8; 32]` as recipient.
  When the user types a destination address, validate it's a valid
  base58 pubkey AND not the system program ID.
- **Sealed salt is one-shot.** The relayer's `/solana/recipient/register-stealth`
  generates a fresh salt every call. If the FE caches the response,
  cache it correctly — losing it means losing access to any funds
  delivered to that stealth PDA.
- **Amount precision.** Always serialize u64 amounts as strings (not
  JS numbers) when round-tripping through the relayer's HTTP API. The
  Anchor program rejects `0` for `expectedAmountB`/`amountA`/`reward`.

---

## 12. Local dev / devnet config

For testing against Solana devnet:

```bash
# 1. deploy bridge_intent to devnet (one-time)
cd solana && anchor deploy --provider.cluster devnet

# 2. mint test tokens
cd ../tachyon-prod
SOLANA_RPC_URL=https://api.devnet.solana.com \
INITIAL_MINT_TO=<your test wallet> \
go run ./cmd/deploy-spl-test-tokens

# 3. wire .env (uncomment CHAIN_4 block in env.example)
#    set CHAIN_COUNT=4
#    fill CHAIN_4_SOLANA_USDC_MINT from solana/deployments/devnet.json
#    set SOLVER_SOLANA_PRIVATE_KEY to the solver's devnet keypair

# 4. start backend
go run ./cmd/relayer
go run ./cmd/solver
```

The relayer logs `Solana endpoints enabled at /solana/*` when at least
one Solana chain is configured.

---

## 13. What's NOT shipped on backend (intentional)

The FE doesn't need to wait for these — they're operational items:

- **Mainnet program redeploy.** The Anchor program ID `64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk`
  in this repo was generated locally; for production, a freshly
  generated program keypair will be deployed and the env templates
  updated. FE behavior is identical; only the `programId` changes.
- **Helius enhanced-webhook → solver dispatch.** The endpoint accepts
  and authenticates webhook posts but currently only logs them; the
  WS-based listener in [pkg/blockchain/solana_client.go](pkg/blockchain/solana_client.go)
  is the active event source. Phase 5 swaps to webhook-driven for
  production.
- **Cancel-intent rent reclaim.** The Anchor program's `cancel_intent`
  doesn't currently `close` the IntentRecord/IntentBids accounts back
  to the user; rent stays locked. UX-only; no security impact.
- **End-to-end devnet test harness.** A scripted EVM→Solana round-trip
  test exists at [solana/tests/bridge_intent.ts](solana/tests/bridge_intent.ts)
  for the Solana side; the EVM↔Solana cross-chain integration test
  lives at [test/e2e/complete_flow.go](test/e2e/complete_flow.go) +
  [test/e2e/complete_flow_solana.go](test/e2e/complete_flow_solana.go)
  and runs all 4 directional flows.

## 14. Operator setup additions (Phase 4 wiring)

This section is for the operator deploying the backend, not the FE.
With the Phase 4 changes the relayer can drive `settle_intent_with_chain2_verification`
on Solana, completing the Solana→EVM round-trip:

### One-time on-chain authorization

```bash
# 1. Generate a relayer Solana keypair (separate from solver's).
solana-keygen new --outfile ~/.config/solana/relayer-solana.json --no-bip39-passphrase

# 2. Convert to base58 for env.
cd tachyon-prod
go run ./cmd/solana-key-to-base58 ~/.config/solana/relayer-solana.json
# stdout = base58 secret → paste into RELAYER_SOLANA_PRIVATE_KEY in .env
# stderr = pubkey

# 3. Authorize the relayer pubkey on-chain via the migration.
cd ../solana
export BRIDGE_INTENT_CHAIN_ID=40168
export BRIDGE_INTENT_USDC_MINT=<from solana/deployments/devnet.json>
export BRIDGE_INTENT_WSOL_MINT=So11111111111111111111111111111111111111112
export BRIDGE_INTENT_RELAYER_PUBKEY=<pubkey from step 2>
anchor migrate --provider.cluster devnet
```

The migration is idempotent — it skips `initialize` if config already
exists, and skips `add_relayer` if the pubkey is already authorized.

### What the relayer does once authorized

- `/settle` POSTs whose source chain is Solana now branch into
  `settleSolanaSourceIntent` (see [pkg/relayer/solana_settle.go](pkg/relayer/solana_settle.go)),
  which verifies the EVM-side delivery via `IsIntentSolvedOnChain2`,
  then submits the Anchor `settle_intent_with_chain2_verification`
  instruction signed by `RELAYER_SOLANA_PRIVATE_KEY`.
- `/verify` and the verification step inside `/settle` now also handle
  Solana destinations transparently — the FE doesn't need to know
  whether the destination is EVM or Solana.

If `RELAYER_SOLANA_PRIVATE_KEY` is left unset, the relayer still serves
all read endpoints but `/settle` for Solana-source intents fails fast
with a clear error.

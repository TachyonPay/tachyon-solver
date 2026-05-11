# Tachyon Stealth Bridge — Frontend Integration Guide

End-to-end FE spec for integrating the Tachyon bridge with stealth on
both EVM and Solana. Every endpoint, when to call it, what to sign,
and how it maps to the equivalent on the other chain.

Audience: a frontend engineer who's never touched this codebase and
needs to add Tachyon-stealth to a dApp.

---

## 1. Mental model in 30 seconds

**The Tachyon bridge does**: user creates a cross-chain "intent" on
chain A, a solver fulfills it on chain B, a relayer verifies + settles
on chain A. Standard intent-based bridge.

**Stealth adds privacy**: instead of delivering to the user's main
wallet on chain B, the solver delivers to a fresh "stealth address"
that only the user can later claim. Chain-watchers can't link
chain-A-sender to chain-B-recipient.

**Both chains use the same trust model**:
- **Relayer (TEE-secured aspirationally)** stores a per-recipient
  long-lived secret
- **User wallet** signs to authenticate retrieval of per-intent
  stealth material
- **Claim** requires user wallet signature on-chain

The cryptographic plumbing differs (Pedersen-style on EVM, HMAC+PDA
on Solana), but the FE-facing UX is symmetric.

---

## 2. Endpoint catalog

### 2.1 Bootstrap (call once at app load)

| Endpoint | Method | When | Equivalent |
|---|---|---|---|
| `/solana/program-info` | GET | Once at boot; cache forever | EVM: chains/RPC URLs are FE config |
| `/ecies-pubkey` | GET | Once per session before any `/store-recipients` | Same endpoint on both — relayer-side ECIES key |

```typescript
const programInfo = await fetch(`${RELAYER_URL}/solana/program-info`).then(r => r.json());
// programInfo.chains["40168"] = { programId, usdcMint, wsolMint, configPda, escrowAuthorityPda, seeds, ... }

const eciesPubkey = await fetchECIESPubkey(RELAYER_URL);
// Used to encrypt the recipient list before /store-recipients
```

### 2.2 Recipient registration (one-time per user)

| Solana endpoint | EVM endpoint | When |
|---|---|---|
| `POST /solana/recipient/register` | `POST /recipient/register` | Once per recipient — first time they want to receive on this chain |

Both are **idempotent**: re-register returns the existing account
without generating new keys.

**Solana auth**: ed25519 signature over `"REGISTER_SOLANA_STEALTH:<pubkey>:<timestamp>"`

```typescript
async function registerSolanaRecipient(wallet, relayerUrl) {
  const pk = wallet.publicKey.toBase58();
  const ts = Math.floor(Date.now() / 1000);
  const msg = new TextEncoder().encode(`REGISTER_SOLANA_STEALTH:${pk}:${ts}`);
  const sig = await wallet.signMessage(msg);
  return fetch(`${relayerUrl}/solana/recipient/register`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      recipientPubkey: pk,
      timestamp: ts,
      signature: bytesToHex(sig),
    }),
  }).then(r => r.json());
  // → { success: true, recipientPubkey, alreadyExists: bool }
}
```

**EVM auth**: EIP-712 signature over `RegisterStealth(address signer)`

```typescript
const domain = { name: "TachyonStealth", version: "1" };
const types = { RegisterStealth: [{ name: "signer", type: "address" }] };
const message = { signer: recipientAddress };
const signature = await wallet.signTypedData(domain, types, message);
// POST { recipientAddress, signature }
```

Server side: relayer verifies the sig, generates:
- **Solana**: 32-byte `viewSecret` (HMAC key for deterministic salt derivation)
- **EVM**: `viewPrivKey` + `spendPrivKey` (Pedersen-style stealth derivation)

The per-recipient secret is stored once and reused for every future
intent to that recipient.

### 2.3 Per-intent stealth address generation (per intent)

| Solana endpoint | EVM endpoint | When | Auth |
|---|---|---|---|
| `POST /solana/recipient/generate-salt` | `POST /recipient/generate-stealth` | Before submitting the intent (sender side) | None |

Unauthenticated — the *sender* (or anyone helping the user) can request
a fresh stealth address for any registered recipient. The returned
material alone never enables theft (claim still requires the
recipient's wallet sig).

**Solana** — returns a deterministic salt + derived PDA:

```typescript
const gen = await fetch(`${RELAYER_URL}/solana/recipient/generate-salt`, {
  method: "POST",
  body: JSON.stringify({
    recipientPubkey: userSolanaPubkey,
    intentId: predictedIntentId.toString(),
    chainId: "84532",                                     // source chain ID
    programId: programInfo.chains["40168"].programId,     // dest Solana program
  }),
}).then(r => r.json());
// → { success: true, salt: "0x...", nonce: 3, stealthPda: "..." }
```

The relayer derives `salt = HMAC(viewSecret, intentId || chainId ||
nonce)` and increments the nonce. Salt is returned to the caller and
also recoverable later — **the relayer does not persist the salt**;
only `(intentId, chainId, nonce, stealthPda)` metadata is stored.

**EVM** — returns a stealth EOA address + predicted Safe address:

```typescript
const gen = await fetch(`${RELAYER_URL}/recipient/generate-stealth`, {
  method: "POST",
  body: JSON.stringify({
    recipientAddress: recipientEvmEOA,
    chainId: "84532",
    intentId: predictedIntentId.toString(),
  }),
}).then(r => r.json());
// → { success: true, stealthAddress: "0x...", safeAddress: "0x..." }
```

The relayer derives:
- `ephemeralKey` from `(viewPrivKey, nonce, chainId, intentId)`
- `stealthAddress` = stealth EOA's address (its priv key is computable
  from `spendPrivKey + ephemeralPubKey` later)
- `safeAddress` = predicted CREATE2 Safe address owned by `stealthAddress`

The **safeAddress is the actual recipient in the intent** — the Safe
isn't deployed yet but funds can land at the CREATE2 address.

### 2.4 Multi-recipient encrypted splits (any direction)

If you want to split one intent across N recipients (e.g., 3 stealth
addresses each receiving a portion of the total), encrypt the list
and POST it.

| Endpoint | Method | When | Auth |
|---|---|---|---|
| `POST /store-recipients` | POST | After generating N stealth targets, before submitting the intent | None |
| `GET /get-recipients/:intentId?sourceChainId=:srcId` | GET | Solver only — happens server-to-server, FE never calls this | `X-Solver-Address` header |

```typescript
// Build the recipient list
const recipients = [stealthPda0, stealthPda1, stealthPda2];   // base58 for Solana, 0x-hex for EVM
const amounts = ["30000000", "35000000", "30000000"];          // raw u64/uint256 strings

// Encrypt with relayer's ECIES pubkey (use a JS ECIES lib — eciesjs, etc.)
const encrypted = await eciesEncrypt(eciesPubkey, JSON.stringify({ recipients, amounts }));

await fetch(`${RELAYER_URL}/store-recipients`, {
  method: "POST",
  body: JSON.stringify({
    intentId: predictedIntentId.toString(),
    chainId: sourceChainId,                              // SOURCE chain ID (not dest)
    encryptedData: bytesToHex(encrypted),
  }),
});
```

The solver decrypts via `/get-recipients` after it wins the auction.
Recipient lists for **N=1 (single-recipient)** intents can skip this
step — just put the single address in the intent's `recipient` field
on-chain.

### 2.5 Intent creation (FE → blockchain, not relayer)

This is **not** a relayer call — the FE submits the intent transaction
directly to the source chain.

| Source chain | Function | Recipient field |
|---|---|---|
| EVM (Base/Horizen) → Solana | `BridgeIntent.createIntentToSolana(...)` | `bytes32 recipient` (stealth PDA as 32 raw bytes) |
| EVM (Base/Horizen) → EVM | `BridgeIntent.createIntent(...)` | Not stored on-chain; recipients come from `/store-recipients` |
| Solana → EVM | Anchor `bridge_intent::create_intent(...)` | `[u8; 32]` recipient (Safe address left-padded with 12 zero bytes) |
| Solana → Solana | Anchor `bridge_intent::create_intent(...)` | `[u8; 32]` (stealth PDA's 32 bytes) |

When using multi-recipient encrypted splits, the on-chain `recipient`
field carries the **first** stealth target (contract requires non-zero).
The solver consults `/get-recipients` for the full N-recipient list.

```typescript
// EVM → Solana example
const bridgeBaseAddr = programInfo.evm.baseBridge;
const userSplPubkey = wallet.publicKey;
const stealthPda32 = userSplPubkey.toBytes();             // 32 raw bytes

await bridge.write.createIntentToSolana([
  USDC_ADDR_ON_BASE,
  tokenBMint32,                                          // bytes32 of dest SPL mint
  amountA,                                               // uint256
  expectedAmountB,                                       // uint256 — IN DEST DECIMALS
  reward,                                                // uint256
  SOLANA_DEST_CHAIN_ID,                                  // uint64 (LayerZero EID)
  stealthPda32,                                          // bytes32
  auctionDuration,                                       // uint256
]);
```

```typescript
// Solana → EVM example (with Anchor IDL client)
const safeAddrBytes32 = leftPadEvmAddrTo32(safeAddresses[0]);
await program.methods
  .createIntent(
    new BN(amountA),
    new BN(expectedAmountB),
    new BN(reward),
    new BN(destChainId),                                 // EVM chain ID
    safeAddrBytes32,                                     // [u8; 32]
    new BN(auctionDuration),
  )
  .accounts({
    user: wallet.publicKey,
    tokenAMint: usdcMint,
    tokenBMint: usdcMint,                                // for EVM dest, this is a sentinel
    userTokenAAccount: userATA,
    intentRecord: derivedIntentPda,
    /* ... */
  })
  .rpc();
```

### 2.6 Stealth-address registration on Solana (per intent)

This is **Solana-specific** — has no EVM equivalent. The Anchor program
requires the user to register the stealth PDA on-chain *before* the
sweep can work later (the StealthRegistration PDA stores `user_pubkey
+ salt` so `sweep_stealth` knows who's allowed to drain).

| Instruction | When | Signer |
|---|---|---|
| Anchor `bridge_intent::register_stealth(salt)` | Once per salt, before or alongside intent submission | User's Solana wallet |

```typescript
// Build register_stealth instruction with the returned salt
const ix = await program.methods
  .registerStealth(Array.from(saltBytes))
  .accounts({
    user: wallet.publicKey,
    stealthRegistration: derivedStealthRegistrationPda,
    systemProgram: SystemProgram.programId,
  })
  .instruction();

// Wallet signs + submits
await sendAndConfirm(wallet, [ix]);
```

**Why no EVM equivalent**: EVM stealth uses CREATE2 — no pre-registration
needed because the Safe deployment IS the user's first claim action. On
Solana, you must register first; sweep_stealth fails otherwise.

### 2.7 Recipient claim — retrieving stealth material

**This is where the recipient comes back later** to claim funds.

| Solana endpoint | EVM endpoint | When | Auth |
|---|---|---|---|
| `POST /solana/recipient/get-salts` | `POST /recipient/get-stealth-addresses` | After funds delivered, when recipient wants to claim | Wallet sig over canonical message |
|  | `POST /recipient/derive-stealth-key` | EVM only — get the actual stealth EOA private key | EIP-712 |

**Solana** — recipient signs a canonical message, relayer returns the
list of `{intentId, chainId, salt, stealthPda}` records:

```typescript
const ts = Math.floor(Date.now() / 1000);
const msg = new TextEncoder().encode(`GET_SOLANA_STEALTH:${userPubkey}:${ts}`);
const sig = await wallet.signMessage(msg);

const resp = await fetch(`${RELAYER_URL}/solana/recipient/get-salts`, {
  method: "POST",
  body: JSON.stringify({ recipientPubkey: userPubkey, timestamp: ts, signature: bytesToHex(sig) }),
}).then(r => r.json());

// resp.records = [{ intentId, chainId, salt, stealthPda, createdAt }, ...]
// Salts are RE-DERIVED server-side from viewSecret + nonce — never persisted.
```

**EVM** — two-step:

```typescript
// Step 1: list stealth addresses
const sig1 = signEIP712GetStealthAddresses(wallet, recipientAddress);
const list = await fetch(`${RELAYER_URL}/recipient/get-stealth-addresses`, { ... });

// Step 2: for each one, derive the private key
for (const entry of list.stealthAddresses) {
  const sig2 = signEIP712DeriveStealthKey(wallet, recipientAddress, entry.address);
  const derive = await fetch(`${RELAYER_URL}/recipient/derive-stealth-key`, { ... });
  const stealthEOA = new Wallet(derive.stealthPrivateKey);
  // Use stealthEOA to deploy the Safe at entry.safeAddress
}
```

### 2.8 Final claim — getting the funds out

**Solana** — call `sweep_stealth` per stealth PDA:

```typescript
for (const rec of resp.records) {
  const saltBytes = hexToBytes(rec.salt);
  const stealthPda = derivePDA(["stealth", userPubkey.toBytes(), saltBytes], programId);
  const stealthAta = getATA(stealthPda, USDC_MINT);
  const userAta = getATA(userPubkey, USDC_MINT);

  const ix = await program.methods
    .sweepStealth(Array.from(saltBytes))
    .accounts({
      user: wallet.publicKey,
      stealthRegistration: derivedStealthRegistrationPda,
      stealthPda,
      mint: USDC_MINT,
      stealthAta,
      userAta,
      tokenProgram: TOKEN_PROGRAM_ID,
      associatedTokenProgram: ASSOCIATED_TOKEN_PROGRAM_ID,
    })
    .instruction();

  await sendAndConfirm(wallet, [ix]);
  // → Tokens flow stealthAta → userAta
}
```

**EVM** — deploy the Safe at the predicted CREATE2 address, then spend
via the Safe:

```typescript
// Step 1: Deploy the Safe (relayer can do this via /recipient/deploy-safe,
// or you can deploy yourself if you have the stealth EOA's key)
const sig = signEIP712DeploySafe(stealthEOA, safeAddress, chainId);
await fetch(`${RELAYER_URL}/recipient/deploy-safe`, { ... });

// Step 2: Now the Safe exists, with stealthEOA as owner.
// Issue Safe transactions to move funds to your main wallet:
const safe = new Safe({ address: safeAddress, signer: stealthEOA, ... });
await safe.transferERC20(USDC_BASE, mainWallet, balance);
```

---

## 3. Decimal mismatch — your responsibility

When the source token has fewer decimals than the destination (typical:
Solana SPL USDC 6-dec → Base USDC.e 18-dec mock), the **solver scales
the destination amount automatically** by reading both tokens' decimals
on-chain. You don't need to do anything special on the FE.

**But** you DO need to specify amounts in the right units when creating
the intent:

| Direction | `amountA` units | `expectedAmountB` units |
|---|---|---|
| EVM → Solana | EVM source token's decimals (e.g., 18 for USDC.e) | **Solana destination token's decimals** (e.g., 6 for SPL USDC) |
| Solana → EVM | Solana source token's decimals (6 typical) | **Solana source decimals** — solver scales up on delivery |
| EVM → EVM | Source decimals | **Dest decimals** (FE pre-scales) |
| Solana → Solana | Source decimals | Source decimals — same on both sides |

The reason `Solana→EVM expectedAmountB` is in **source** decimals: the
Anchor program stores it as `u64`, which can't hold 18-decimal values
for typical USD amounts. The solver scales up at delivery time.

---

## 4. Signing primitives reference

### EVM — EIP-712 typed data

All signatures use the same domain:
```
{ name: "TachyonStealth", version: "1" }
```
(intentionally no `chainId` / `verifyingContract` — these are off-chain
auth, not on-chain transactions.)

| Action | Type | Fields |
|---|---|---|
| Register | `RegisterStealth` | `address signer` |
| List stealth addrs | `GetStealthAddresses` | `address signer` |
| Derive stealth key | `DeriveStealthKey` | `address recipientAddress, address stealthAddress` |
| Deploy Safe | `DeploySafe` | `address stealthAddress, uint256 chainId` |

```typescript
const signature = await wallet.signTypedData(
  { name: "TachyonStealth", version: "1" },
  { RegisterStealth: [{ name: "signer", type: "address" }] },
  { signer: walletAddress },
);
```

### Solana — ed25519 raw bytes

Sign the canonical UTF-8 message bytes; relayer verifies with
`ed25519.Verify`.

| Action | Canonical message |
|---|---|
| Register | `"REGISTER_SOLANA_STEALTH:<base58Pubkey>:<unixSeconds>"` |
| Get salts | `"GET_SOLANA_STEALTH:<base58Pubkey>:<unixSeconds>"` |

```typescript
const msg = new TextEncoder().encode(`REGISTER_SOLANA_STEALTH:${pubkey.toBase58()}:${ts}`);
const sig = await wallet.signMessage(msg);   // Phantom/Solflare/Backpack API
```

Timestamp must be within ±5 minutes of relayer's clock (replay
protection). Always use `Math.floor(Date.now() / 1000)`.

---

## 5. End-to-end flow walkthrough

### Sender side — submitting an intent with stealth

```
[1] At app load:
    GET /solana/program-info        → cache chain metadata
    GET /ecies-pubkey               → cache relayer ECIES pubkey

[2] When user clicks "Send to <recipient>":
    a. Predict the source-chain intent ID (read getLatestIntentId or Config.next_intent_id)
    b. Call /recipient/register or /solana/recipient/register (idempotent — only does work first time)
    c. For each recipient (N=1 for simple, N=3+ for splits):
       call /generate-stealth (EVM) or /generate-salt (Solana)
       → get stealth address(es) + salts
    d. Build encrypted recipient list, POST /store-recipients (only if N > 1)
    e. (Solana only) For each salt, call Anchor register_stealth(salt) on-chain — wallet signs
    f. Submit intent transaction on source chain — wallet signs
       - EVM source: createIntentToSolana or createIntent
       - Solana source: Anchor create_intent

[3] Show user "Bridge in progress"; poll status:
    Solana destination: poll for SolvedRecord PDA at ["solved", srcChainID, intentID]
    EVM destination:    poll bridge.isIntentSolvedOnChain2(srcChainID, intentID)
```

### Recipient side — claiming stealth-delivered funds

```
[1] User connects wallet on the destination chain (could be a different
    device than the sender). Connect Phantom for Solana, MetaMask for EVM.

[2] FE calls /get-salts (Solana) or /get-stealth-addresses (EVM) with wallet sig.
    → returns list of pending stealth records for this user.

[3] For each record, check on-chain balance:
    Solana: getTokenAccountBalance(stealthAta)
    EVM:    USDC.balanceOf(safeAddress)

[4] If balance > 0, show "Claim X USDC" button. On click:
    Solana: build sweep_stealth(salt) ix, wallet signs, broadcast
    EVM:    deploy Safe via /recipient/deploy-safe, then transfer
            from Safe to main wallet

[5] After claim:
    Solana: stealthAta drained, userAta credited
    EVM:    Safe deployed at predicted addr, ownership = stealthEOA;
            recipient can move funds via Safe transactions
```

---

## 6. Error handling reference

| Status | Body error | What it means | FE handling |
|---|---|---|---|
| 400 | `invalid signature hex` | sig couldn't be hex-decoded | Re-sign and retry |
| 400 | `recipient not registered` | `/generate-salt` before `/register` | Call `/register` first |
| 401 | `timestamp out of window` | Clock skew > 5 min | Sync clock or re-generate timestamp |
| 401 | `invalid ed25519 signature` | Sig didn't verify | Confirm canonical message matches exactly (including unix-seconds timestamp) |
| 403 | `access denied` | `/get-recipients` non-solver request | FE should never call this endpoint |
| 404 | `recipient not registered` | Account not in DB | Call register first |
| 404 | `recipients not found for intent X` | No `/store-recipients` was POSTed | Sender should have stored before solver fetched |
| 500 | (any) | Relayer internal error | Show user a "temporarily unavailable" message; retry |

---

## 7. Full endpoint reference (cheat sheet)

| Endpoint | Method | Auth | Caller |
|---|---|---|---|
| `/solana/program-info` | GET | None | FE (boot) |
| `/solana/program-info/:chainId` | GET | None | FE (single-chain query) |
| `/solana/intent/:chainId/:intentId` | GET | None | FE (read IntentRecord) |
| `/solana/intent/:chainId/:intentId/solved` | GET | None | FE/anyone (solve check) |
| `/solana/solved/:srcChainId/:srcIntentId` | GET | None | FE (cross-chain solve check) |
| `/solana/helius-webhook` | POST | `Authorization: <secret>` | Helius (events) |
| `/solana/recipient/register` | POST | ed25519 sig | FE (one-time) |
| `/solana/recipient/generate-salt` | POST | None | FE (sender, per intent) |
| `/solana/recipient/get-salts` | POST | ed25519 sig | FE (recipient, claim) |
| `/solana/recipient/register-stealth` | POST | ed25519 sig | (deprecated; use generate-salt) |
| `/solana/recipient/store-salt` | POST | None | (legacy; deprecated) |
| `/solana/recipient/derive-stealth-pda` | POST | None | FE utility |
| `/recipient/register` | POST | EIP-712 | FE (EVM recipient, one-time) |
| `/recipient/generate-stealth` | POST | None | FE (sender, per intent) |
| `/recipient/get-stealth-addresses` | POST | EIP-712 | FE (recipient, list) |
| `/recipient/derive-stealth-key` | POST | EIP-712 | FE (recipient, get priv key) |
| `/recipient/deploy-safe` | POST | EIP-712 | FE (recipient, Safe deploy) |
| `/store-recipients` | POST | None | FE (sender, encrypted N>1) |
| `/get-recipients/:intentId` | GET | `X-Solver-Address` | Solver only |
| `/ecies-pubkey` | GET | None | FE (boot) |
| `/settle` | POST | (relayer-internal) | Solver |
| `/intent-details/:intentId` | GET | None | FE (status polling) |
| `/register-solver` | POST | None | Solver (boot) |

---

## 8. EVM ↔ Solana cross-reference

| Concept | EVM | Solana |
|---|---|---|
| Recipient register endpoint | `/recipient/register` | `/solana/recipient/register` |
| Register signing scheme | EIP-712 ECDSA | ed25519 over UTF-8 message |
| Per-recipient secret at relayer | `viewPrivKey` + `spendPrivKey` (two secp256k1 keys) | `viewSecret` (single 32-byte HMAC key) |
| Per-intent stealth derivation | TEE-derived Pedersen: `stealthAddr = pubkey(ephemeral + spendPub)` | HMAC + PDA: `salt = HMAC(viewSecret, intentId, ...) ; PDA = derive([stealth, user, salt], programId)` |
| On-chain pre-registration needed? | No (CREATE2 is offline) | **Yes** — `register_stealth(salt)` must run before sweep |
| On-chain recipient field | `bytes32 _recipient` (`createIntentToSolana`) or none (`createIntent`, uses `/store-recipients`) | `[u8; 32] recipient` (Anchor `create_intent`) |
| Solver delivery primitive | `bridge.solveIntentOnChain2(recipients[], amounts[])` | Anchor `solve_intent_on_chain2` with `remaining_accounts` array |
| Recipient retrieval auth | EIP-712 sig | ed25519 sig |
| What recipient retrieves | Stealth EOA private key (via `/derive-stealth-key`) | Per-intent salts (via `/get-salts`) |
| Final claim mechanism | Deploy Safe via stealth EOA → transfer from Safe to main wallet | `sweep_stealth(salt)` signed by user wallet → transfer from stealthATA to userATA |
| Recoverability from wallet alone | ✓ (sign + retrieve stealth keys + deploy Safe) | ✓ (sign + retrieve salts + sweep) |
| Replay protection | EIP-712 domain separators | timestamp ± 5 min window |

---

## 9. Quick start TypeScript snippets

### Setup (one-time at app load)

```typescript
import { fetchECIESPubkey, eciesEncrypt } from "your-ecies-utils";
import { PublicKey, Connection } from "@solana/web3.js";

const RELAYER_URL = "http://localhost:8080"; // prod: https://relayer.tachyon.example
const programInfo = await fetch(`${RELAYER_URL}/solana/program-info`).then(r => r.json());
const eciesPubkey = await fetchECIESPubkey(RELAYER_URL);
const solanaConn = new Connection(programInfo.chains["40168"].rpcUrl);
```

### Recipient register (Solana)

```typescript
async function registerSolanaRecipient(wallet, relayerUrl) {
  const pk = wallet.publicKey.toBase58();
  const ts = Math.floor(Date.now() / 1000);
  const msg = new TextEncoder().encode(`REGISTER_SOLANA_STEALTH:${pk}:${ts}`);
  const sig = await wallet.signMessage(msg);
  const resp = await fetch(`${relayerUrl}/solana/recipient/register`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ recipientPubkey: pk, timestamp: ts, signature: bytesToHex(sig) }),
  });
  if (!resp.ok) throw new Error(`register failed: ${await resp.text()}`);
  return resp.json(); // { success, alreadyExists }
}
```

### Recipient register (EVM)

```typescript
async function registerEvmRecipient(wallet, relayerUrl) {
  const signature = await wallet.signTypedData(
    { name: "TachyonStealth", version: "1" },
    { RegisterStealth: [{ name: "signer", type: "address" }] },
    { signer: wallet.address },
  );
  const resp = await fetch(`${relayerUrl}/recipient/register`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      recipientAddress: wallet.address,
      signature: signature.replace(/^0x/, ""),
    }),
  });
  if (!resp.ok) throw new Error(`register failed: ${await resp.text()}`);
  return resp.json();
}
```

### Claim Solana stealth funds

```typescript
async function claimSolanaStealth(wallet, relayerUrl, anchorProgram) {
  // 1. Sign retrieval request
  const pk = wallet.publicKey.toBase58();
  const ts = Math.floor(Date.now() / 1000);
  const msg = new TextEncoder().encode(`GET_SOLANA_STEALTH:${pk}:${ts}`);
  const sig = await wallet.signMessage(msg);

  // 2. Fetch all pending stealth records
  const resp = await fetch(`${relayerUrl}/solana/recipient/get-salts`, {
    method: "POST",
    body: JSON.stringify({ recipientPubkey: pk, timestamp: ts, signature: bytesToHex(sig) }),
  }).then(r => r.json());

  // 3. For each, check balance and sweep if non-zero
  for (const rec of resp.records) {
    const saltBytes = hexToBytes(rec.salt);
    const stealthPda = new PublicKey(rec.stealthPda);
    const stealthAta = getAssociatedTokenAddressSync(USDC_MINT, stealthPda, true);

    const balance = await connection.getTokenAccountBalance(stealthAta).catch(() => null);
    if (!balance || balance.value.amount === "0") continue;

    // Build + send sweep_stealth tx
    await anchorProgram.methods
      .sweepStealth(Array.from(saltBytes))
      .accounts({ user: wallet.publicKey, /* ... */ })
      .rpc();

    console.log(`Swept ${balance.value.amount} from ${rec.stealthPda}`);
  }
}
```

---

## 10. UX patterns

- **Show "Pending claims" badge**: poll `/get-salts` (Solana) +
  `/get-stealth-addresses` (EVM) periodically; show count of non-zero
  balances. One-click claim flow.
- **One register button**: at first connect, call both `/recipient/register`
  (EVM) AND `/solana/recipient/register` (Solana) so the user is set
  up everywhere.
- **Cache stealth records in localStorage** so the user can see pending
  balances without re-signing on every load. Re-fetch on user action
  ("Refresh balances").
- **Surface the trust model**: "Your funds are stored at addresses only
  you can claim. The relayer holds bookkeeping; only your wallet seed
  can move funds." Builds confidence.
- **Don't show raw salts/keys to users**: they're FE-internal. The FE
  derives PDAs, calls relayer, broadcasts txs — the user just sees
  USD amounts and clicks "Claim."

---

## 11. Production checklist

Before going live with real money:

- [ ] Relayer running inside a TEE (sealed `viewSecret` / `viewPrivKey+spendPrivKey`)
- [ ] HTTPS with valid cert (auth-bearing endpoints must not be in clear)
- [ ] CORS configured for your FE origin only
- [ ] Rate limiting (already on `/store-recipients` at 30 req/min — verify yours)
- [ ] Relayer DB backups (the per-recipient secrets are the load-bearing data)
- [ ] Monitoring on `/get-recipients` 403 rates (auth failures = potentially malicious)
- [ ] Replay-protection clock-sync verified (NTP)
- [ ] Recipient claim-time UX clearly shows source-of-funds (don't let users sweep without seeing what intent it came from)
- [ ] Wallet-disconnect resets all FE state (no leaked sigs across users)
- [ ] Generated stealth addresses + salts NEVER logged anywhere except inside the TEE

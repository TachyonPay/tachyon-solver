# Lucky Draw — Frontend Integration Guide

After a user bridges via Tachyon and derives their stealth key, they can enter a one-time lucky draw for a random 5–10 USDC bonus. This guide covers everything the frontend needs: when to show the UI, how to sign, which endpoints to call, and how to handle the response.

---

## What you need before calling the lucky draw

The lucky draw requires the proxy Safe to be deployed on-chain (so `getOwners()` can be called). Make sure these steps have already happened in your bridge flow before showing the entry UI:

1. Bridge settled — solver delivered USDC to the proxy Safe address
2. Stealth key derived — user called `POST /recipient/derive-stealth-key` and your app has the stealth private key in memory
3. Proxy Safe deployed — user called `POST /recipient/deploy-safe` and the tx confirmed

If the Safe is not deployed, the service will reject with `ownership check failed`.

---

## 3-step integration

### Step 1 — Check if already entered

On page load (or when showing the lucky draw card), check whether this stealth address has already entered. This prevents showing the UI to users who already used their shot.

```
GET /lucky-draw/status?stealth={stealthAddress}
```

**Response — not entered:**
```json
{
  "success": true,
  "status": {
    "entered": false,
    "stealthAddress": "0x..."
  }
}
```

**Response — already entered:**
```json
{
  "success": true,
  "status": {
    "entered":        true,
    "won":            true,
    "stealthAddress": "0x...",
    "proxyAddress":   "0x...",
    "chainId":        "84532",
    "amountUSDC":     7,
    "txHash":         "0x...",
    "enteredAt":      1716000000
  }
}
```

If `entered: true` — show the result card (won/lost) instead of the entry button.

---

### Step 2 — Sign the EIP-712 message

The entry must be signed with the **stealth private key** (the key the TEE gave back from `derive-stealth-key`), not the user's MetaMask wallet.

**Domain:**
```js
{
  name:    "TachyonStealth",
  version: "1"
  // no chainId, no verifyingContract — off-chain domain
}
```

**Types:**
```js
{
  LuckyDrawEntry: [
    { name: "proxyAddress", type: "address" },
    { name: "chainId",      type: "uint256" }
  ]
}
```

**Message:**
```js
{
  proxyAddress: checksumAddress(proxyAddress),  // EIP-55 checksum
  chainId:      BigInt(chainId)                 // destination chain of the bridge
}
```

#### ethers.js v6

```ts
import { Wallet, TypedDataEncoder, getAddress } from "ethers";

async function signLuckyDrawEntry(
  stealthPrivKey: string,   // hex from derive-stealth-key, with or without 0x
  proxyAddress:   string,
  chainId:        string
): Promise<string> {
  const wallet = new Wallet(stealthPrivKey);

  const domain = { name: "TachyonStealth", version: "1" };

  const types = {
    LuckyDrawEntry: [
      { name: "proxyAddress", type: "address" },
      { name: "chainId",      type: "uint256" },
    ],
  };

  const message = {
    proxyAddress: getAddress(proxyAddress),
    chainId:      BigInt(chainId),
  };

  return wallet.signTypedData(domain, types, message);
  // returns 0x-prefixed 65-byte hex, v=27 or 28
}
```

#### viem

```ts
import { privateKeyToAccount } from "viem/accounts";
import { getAddress }          from "viem";

async function signLuckyDrawEntry(
  stealthPrivKey: `0x${string}`,
  proxyAddress:   string,
  chainId:        string
): Promise<`0x${string}`> {
  const account = privateKeyToAccount(stealthPrivKey);

  return account.signTypedData({
    domain: { name: "TachyonStealth", version: "1" },
    types: {
      LuckyDrawEntry: [
        { name: "proxyAddress", type: "address" },
        { name: "chainId",      type: "uint256" },
      ],
    },
    primaryType: "LuckyDrawEntry",
    message: {
      proxyAddress: getAddress(proxyAddress) as `0x${string}`,
      chainId:      BigInt(chainId),
    },
  });
}
```

> **Note on `v`:** Both ethers and viem produce `v=27` or `v=28` natively. The service accepts both. Do not manually adjust `v`.

---

### Step 3 — Submit the entry

```
POST /lucky-draw/enter
Content-Type: application/json
```

**Body:**
```json
{
  "proxyAddress": "0xYourProxySafeAddress",
  "chainId":      "84532",
  "signature":    "0x..."
}
```

| Field | Value |
|---|---|
| `proxyAddress` | The proxy Safe on the destination chain |
| `chainId` | Decimal string — the chain where the proxy Safe lives |
| `signature` | 65-byte EIP-712 sig, `0x`-prefixed hex |

**Response — won:**
```json
{
  "success":        true,
  "won":            true,
  "stealthAddress": "0x...",
  "proxy":          "0x...",
  "chainId":        "84532",
  "amountUSDC":     7,
  "amountRaw":      "7000000",
  "txHash":         "0x..."
}
```

**Response — entered but did not win:**
```json
{
  "success":        true,
  "won":            false,
  "stealthAddress": "0x...",
  "proxy":          "0x...",
  "chainId":        "84532",
  "amountUSDC":     0,
  "amountRaw":      "0",
  "txHash":         ""
}
```

**Error responses:**

| HTTP | `error` field | Meaning |
|---|---|---|
| 400 | `chain X not supported` | `chainId` not in server config |
| 400 | `ownership check failed` | Safe not deployed or sig doesn't match an owner |
| 400 | `stealth address ... has already entered` | Already used their one shot |
| 400 | `signature recovery failed` | Malformed sig or wrong domain/type |

---

## Full TypeScript example

```ts
const LUCKY_DRAW_URL = "https://your-lucky-draw-service.com";

async function runLuckyDraw(
  stealthPrivKey: string,
  proxyAddress:   string,
  chainId:        string
) {
  // 1. Check status
  const stealthAddress = new Wallet(stealthPrivKey).address;
  const statusRes = await fetch(
    `${LUCKY_DRAW_URL}/lucky-draw/status?stealth=${stealthAddress}`
  );
  const { status } = await statusRes.json();

  if (status.entered) {
    return { alreadyEntered: true, ...status };
  }

  // 2. Sign
  const signature = await signLuckyDrawEntry(stealthPrivKey, proxyAddress, chainId);

  // 3. Enter
  const enterRes = await fetch(`${LUCKY_DRAW_URL}/lucky-draw/enter`, {
    method:  "POST",
    headers: { "Content-Type": "application/json" },
    body:    JSON.stringify({ proxyAddress, chainId, signature }),
  });

  if (!enterRes.ok) {
    const { error } = await enterRes.json();
    throw new Error(`Lucky draw rejected: ${error}`);
  }

  return enterRes.json();
}
```

---

## UI states

```
Bridge complete + Safe deployed
        ↓
status.entered = false  →  show "Enter Lucky Draw" button
status.entered = true   →  show result card (skip entry)
        ↓
User clicks enter
        ↓
POST /lucky-draw/enter
        ↓
won = true   →  "You won N USDC! 🎉  txHash: 0x..."
won = false  →  "Better luck next time — you're entered, one shot used"
```

---

## Which chainId to use

Pass the chain where the user's proxy Safe lives — the destination chain of their bridge. This is the same `chainId` the user passed to `POST /recipient/deploy-safe`.

Supported values are whatever the service operator has configured (check `GET /lucky-draw/health` for the `supportedChains` list).

```
GET /lucky-draw/health
```
```json
{
  "status":          "ok",
  "service":         "lucky-draw",
  "supportedChains": ["2651420", "84532"],
  "range":           { "minUSDC": 5, "maxUSDC": 10 }
}
```

---

## Privacy notes for the FE

- The stealth private key must **stay in memory only** — never persist it to localStorage or send it to any server other than the TEE-backed relayer.
- The lucky draw service learns `stealthAddress → proxyAddress` from each entry. This is stored server-side and visible to the admin only. It does **not** expose the user's original wallet address.
- `txHash` in the winning response is a public on-chain transfer from the team Safe to the proxy Safe — the user can verify it on any block explorer.

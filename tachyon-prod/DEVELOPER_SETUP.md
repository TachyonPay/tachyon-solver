# Developer Setup — From Clone to Running E2E Tests

This walks a fresh developer through everything needed to run the Tachyon
bridge stack locally and execute the EVM ↔ Solana e2e tests. Assumes the
program is already deployed on Solana devnet and the EVM contracts are
already deployed on Horizen testnet + Base Sepolia.

If you need to redeploy from scratch (different program ID, different
contract addresses), see the "Redeploying" section at the bottom.

---

## 1. Prerequisites

Install all of these. Versions matter — the build uses specific Anchor +
Solana CLI versions.

| Tool | Min Version | Install |
|---|---|---|
| **Go** | 1.24.2+ | https://go.dev/dl |
| **Node.js** | 22+ (works on 23) | https://nodejs.org or `nvm install 22` |
| **Yarn** | 1.22+ | `npm install -g yarn` |
| **Rust** | 1.88+ | `curl https://sh.rustup.rs -sSf \| sh` |
| **Solana CLI** | 3.1.12+ | `sh -c "$(curl -sSfL https://release.anza.xyz/stable/install)"` |
| **Anchor CLI** | exactly **0.32.0** | `cargo install --git https://github.com/coral-xyz/anchor avm --force && avm install 0.32.0 && avm use 0.32.0` |
| **spl-token CLI** | latest | `cargo install spl-token-cli` |
| **Hardhat (via npm)** | comes with repo | `npm install` at repo root |

Verify:

```bash
go version
node --version
rustc --version
solana --version       # should print 3.1.x
anchor --version       # should print 0.32.0
spl-token --version
```

---

## 2. Clone the repo

```bash
git clone <repo-url> zk-tachyon-solver
cd zk-tachyon-solver
```

---

## 3. Generate your local Solana keypairs

The previous dev's `.env` references *their* Solana wallets. **You need
your own** — never use someone else's keypair. Each dev runs against
their own funded wallets.

```bash
cd tachyon-prod

# Solver wallet — places bids, delivers funds, picks up settle payouts
solana-keygen new --outfile solver-solana.json --no-bip39-passphrase

# Relayer wallet — signs settle_intent_with_chain2_verification on Solana
solana-keygen new --outfile relayer-solana.json --no-bip39-passphrase

# (Optional) User wallet — separate from solver, plays "test user" role.
# If you skip this, the test reuses solver-solana.json as the user.
solana-keygen new --outfile user-solana.json --no-bip39-passphrase
```

Print pubkeys (you'll need them):

```bash
solana-keygen pubkey solver-solana.json
solana-keygen pubkey relayer-solana.json
solana-keygen pubkey user-solana.json    # if you generated it
```

Convert each to the base58 secret-key format the Go config expects:

```bash
go run ./cmd/solana-key-to-base58 solver-solana.json
go run ./cmd/solana-key-to-base58 relayer-solana.json
go run ./cmd/solana-key-to-base58 user-solana.json    # if applicable
```

Save the base58 outputs — they go into `.env` in the next step.

> **Security**: the `.gitignore` covers `*-solana.json` files. They're
> never committed. If you ever leak one, the wallet is permanently
> compromised — generate a new one and move funds.

---

## 4. Fund the wallets

### Solana devnet (free)

```bash
solana config set --url https://api.devnet.solana.com   # or your Helius URL

solana airdrop 2 $(solana-keygen pubkey solver-solana.json)
solana airdrop 2 $(solana-keygen pubkey relayer-solana.json)
solana airdrop 2 $(solana-keygen pubkey user-solana.json)   # if applicable
```

Devnet airdrop is rate-limited; if it fails, retry or use
https://faucet.solana.com.

### Get test SPL USDC for the solver

Two options:

**Option A — Use the existing test mint** (shared across the team).
The `.env` already has `CHAIN_3_SOLANA_USDC_MINT` set. Ask another
dev to send you some test USDC, or mint to your address if you have
mint authority over `CLQdHANA2Z2tjka21NqBkzoytaH18JtA2Q56Bt4tHPj`.

**Option B — Deploy your own test mints** (if you want isolation):

```bash
cd tachyon-prod
export SOLANA_RPC_URL=https://api.devnet.solana.com   # or your Helius URL
export DEPLOYER_KEYPAIR_PATH=$HOME/.config/solana/id.json
export INITIAL_MINT_TO=$(solana-keygen pubkey solver-solana.json)

go run ./cmd/deploy-spl-test-tokens
# → writes solana/deployments/devnet.json with the new mint addresses.
# Update CHAIN_3_SOLANA_USDC_MINT in your .env to match.
```

### EVM testnets (Horizen + Base Sepolia)

You'll need:
- **Base Sepolia ETH** for gas: https://www.alchemy.com/faucets/base-sepolia
- **Horizen Gobi testnet HZN** for gas: faucet on the Horizen testnet site
- **Test USDC.e** on Horizen at `TOKEN_USDC_HORIZEN` address
- **Test USDC.e** on Base at `TOKEN_USDC_BASE` address

For the mock USDC, you can either:
- Use the existing tokens at `0xF37dF50...e569` (whoever has mint authority can give you some)
- Deploy your own via the `MockERC20` contract in `contracts/`

Get the EVM private key associated with `PRIVATE_KEY` in `.env` funded
on both Base Sepolia and Horizen testnet with native gas tokens + the
respective mock USDC tokens. Use Etherscan/Blockscout to verify
balances.

---

## 5. Set up `.env`

```bash
cd tachyon-prod
cp env.example .env
```

Edit `.env` and set:

| Key | Value |
|---|---|
| `PRIVATE_KEY` | Your EVM private key (no `0x` prefix). Funded on Horizen + Base Sepolia. |
| `SOLVER_SOLANA_PRIVATE_KEY` | Base58 from step 3 (solver-solana.json) |
| `RELAYER_SOLANA_PRIVATE_KEY` | Base58 from step 3 (relayer-solana.json) |
| `USER_SOLANA_PRIVATE_KEY` | Base58 from step 3 (user-solana.json) — optional |
| `CHAIN_COUNT` | `3` |
| `CHAIN_1_*` (Horizen) | Should already match `env.example` |
| `CHAIN_2_*` (Base Sepolia) | Should already match. Set `CHAIN_2_RPC` to your own Alchemy/QuickNode URL if the public RPC is flaky |
| `CHAIN_3_*` (Solana devnet) | Should already match |
| `CHAIN_3_RPC` | `https://devnet.helius-rpc.com/?api-key=<YOUR_HELIUS_KEY>` — get a free Helius key, public Solana devnet RPC drops connections constantly |
| `CHAIN_3_HELIUS_RPC` | `wss://devnet.helius-rpc.com/?api-key=<YOUR_HELIUS_KEY>` |
| `TOKEN_USDC_HORIZEN` | Address of mock USDC on Horizen |
| `TOKEN_USDC_BASE` | Address of mock USDC on Base Sepolia |
| `MIN_PROFIT_MARGIN` | `0.02` (2%) |
| `TEST_AMOUNT_A` | `100` |
| `TEST_AMOUNT_B` | `95` |
| `TEST_REWARD` | `5` |
| `TEST_RECIPIENT_1_AMOUNT` | `30` (for 3-recipient stealth tests) |
| `TEST_RECIPIENT_2_AMOUNT` | `35` |
| `TEST_RECIPIENT_3_AMOUNT` | `30` |

> The `.env` is gitignored. Each dev maintains their own.

---

## 6. Install JS dependencies

```bash
# Hardhat + ethers (at repo root, for contract deploys)
cd /path/to/zk-tachyon-solver
npm install

# Anchor migration scripts (TypeScript)
cd solana
yarn install
cd ..
```

---

## 7. Verify Solana program state

The bridge_intent program is already deployed on devnet at
`64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk`. Check it:

```bash
solana program show 64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk --url devnet
# Should print authority, last deployed slot, etc.
```

Check the `Config` PDA is initialized:

```bash
# The Config PDA is deterministic; for our program + chainId=40168:
# (you can also derive it programmatically — see migrations/deploy.ts)
solana account EDRkrGbTebr8naq9FQavxhUiz942bKbJeASXmQa9jhu5 --url devnet
# Should print account data, not "AccountNotFound"
```

If the Config PDA exists, the program is initialized and you can skip the
"Redeploying" section.

### Authorize your relayer wallet on-chain (one-time per dev)

The Solana program checks an on-chain `RelayerAuth` PDA before letting any
key call `settle_intent_with_chain2_verification`. Authorize yours:

```bash
cd solana
export BRIDGE_INTENT_CHAIN_ID=40168
export BRIDGE_INTENT_USDC_MINT=CLQdHANA2Z2tjka21NqBkzoytaH18JtA2Q56Bt4tHPj   # from .env CHAIN_3_SOLANA_USDC_MINT
export BRIDGE_INTENT_WSOL_MINT=A39z2AC9QeYAQXk28gKqu1nJB9qkw6go3dSgEVbV3Myd   # from .env CHAIN_3_SOLANA_WSOL_MINT
export BRIDGE_INTENT_RELAYER_PUBKEY=$(solana-keygen pubkey ../tachyon-prod/relayer-solana.json)
export ANCHOR_PROVIDER_URL="https://devnet.helius-rpc.com/?api-key=<YOUR_KEY>"

anchor migrate --provider.cluster devnet
```

Idempotent — re-runs print `already done, skipping` for the init steps
and only run `add_relayer` if your relayer pubkey isn't already
authorized.

---

## 8. Start the services

Open **two terminals**.

**Terminal 1 — Relayer**

```bash
cd tachyon-prod
go run ./cmd/relayer
```

Watch for:
- `Configuration loaded chains=3`
- `Solana client initialized chainID=40168`
- `Relayer solana client initialized mode="signing (settle-capable)"`
- `HTTP server listening on :8080`

**Terminal 2 — Solver**

```bash
cd tachyon-prod
go run ./cmd/solver
```

Watch for:
- `Starting event polling currentBlock=... network=84532` (Base)
- `Starting event polling currentBlock=... network=2651420` (Horizen)
- `Solana driver: subscribed to bridge_intent program logs`

If either crashes, paste the error and check `.env` values + funded
wallets.

---

## 9. Run the e2e tests

**Terminal 3 — test runner**

```bash
cd tachyon-prod/test/e2e

# Non-stealth (single-recipient direct delivery, simpler to debug)
go run base_solana_flow.go

# Stealth (multi-recipient encrypted splits with HMAC-derived salts)
E2E_USE_STEALTH=1 go run base_solana_flow.go
```

What it does:
1. **Test 1** — Base → Solana: user pays USDC.e on Base via `createIntentToSolana`, solver delivers to N stealth PDAs on Solana, user sweeps each PDA back to their wallet
2. **Test 2** — Solana → Base: user pays SPL USDC on Solana via Anchor `create_intent`, solver delivers to N predicted Safe addresses on Base

Both tests print before/after balances + deltas so you can verify the
funds actually moved.

---

## 10. Common pitfalls

| Symptom | Cause | Fix |
|---|---|---|
| `failed to get nonce: 500` | Public Base Sepolia RPC flaking | Use Alchemy/QuickNode; update `CHAIN_2_RPC` |
| `Blockhash not found` | Solana devnet RPC drift | Use Helius (already in `.env`); restart solver |
| `oracle priceA failed, skipping` | Public price API rate-limited | Solver has 1-source-with-sanity-band fallback; if still failing, try again in 1 min |
| `recipient ... not registered` on `/generate-salt` | New dev wallet hasn't registered | Run the test once with `E2E_USE_STEALTH=1` — registration is automatic + idempotent |
| `Deposit not visible yet... network=2651420` for a Solana intent | Stale relayer binary missing the source-chain-isolation fix | `pkill -f bin/relayer && go run ./cmd/relayer` |
| Test 1 fails with `createIntentToSolana tx reverted` | Mock USDC on Base lacks allowance / balance | Check wallet balance + approve enough |

---

## Redeploying (only if you need different contract addresses)

### EVM contracts

```bash
cd /path/to/zk-tachyon-solver
export BASE_SEPOLIA_RPC_URL=https://sepolia.base.org   # or your own
npx hardhat run scripts/deployBaseSepolia.ts --network base_sepolia
# Outputs the new bridge address; update CHAIN_2_BRIDGE in .env.

# Horizen has its own deploy script. If you don't have one, use scripts/deploy.ts
# (deploys both Horizen and Base in one go; will override your existing addrs).
```

### Solana program

**Don't redeploy** unless you specifically need a different program ID.
Redeploying changes the program ID → breaks all existing `Config`,
`StealthRegistration`, and `IntentRecord` PDAs.

If you must:

```bash
cd solana

# Generate fresh program keypair
solana-keygen new --outfile target/deploy/bridge_intent-keypair.json --no-bip39-passphrase
NEW_ID=$(solana-keygen pubkey target/deploy/bridge_intent-keypair.json)

# Update declare_id! in programs/bridge_intent/src/lib.rs
# Update Anchor.toml [programs.*]
# Update CHAIN_3_SOLANA_PROGRAM_ID and SOLANA_PROGRAM_ID in tachyon-prod/.env

anchor build
anchor deploy --provider.cluster devnet

# Then re-run migrate to initialize Config + add_relayer:
export BRIDGE_INTENT_CHAIN_ID=40168
export BRIDGE_INTENT_USDC_MINT=<new mint address>
export BRIDGE_INTENT_RELAYER_PUBKEY=$(solana-keygen pubkey ../tachyon-prod/relayer-solana.json)
anchor migrate --provider.cluster devnet
```

**Critical**: back up `solana/target/deploy/bridge_intent-keypair.json`
OUT-OF-REPO. It's the program upgrade authority — losing it permanently
locks you out of upgrading the program.

```bash
cp solana/target/deploy/bridge_intent-keypair.json ~/Documents/tachyon-program-keypair-BACKUP.json
```

---

## Where things live

```
zk-tachyon-solver/
├── contracts/                      # Solidity sources (BridgeIntent.sol, MockERC20.sol)
├── solana/                         # Anchor workspace
│   ├── programs/bridge_intent/    # Rust program
│   ├── migrations/deploy.ts       # initialize + add_relayer
│   ├── idl/bridge_intent.json     # IDL for the FE
│   └── target/                    # Build artifacts (gitignored)
├── scripts/                        # Hardhat deploy scripts
└── tachyon-prod/
    ├── cmd/
    │   ├── relayer/               # Relayer service entrypoint
    │   ├── solver/                # Solver service entrypoint
    │   ├── deploy-spl-test-tokens/ # CLI: deploy test SPL mints on devnet
    │   └── solana-key-to-base58/  # CLI: keypair JSON → base58 secret
    ├── pkg/
    │   ├── api/                   # HTTP handlers
    │   ├── blockchain/            # EVM + Solana clients
    │   ├── relayer/               # Relayer logic
    │   ├── solver/                # Solver logic
    │   ├── stealth/               # EVM stealth primitives
    │   └── types/                 # Shared types
    └── test/e2e/
        ├── base_solana_flow.go    # Base ↔ Solana e2e test
        ├── complete_flow.go        # Full EVM ↔ EVM ↔ Solana e2e
        └── utils/                  # Test helpers
```

---

## Quick command cheatsheet

```bash
# After clone, the bare minimum to get running:
npm install
cd solana && yarn install && cd ..
cd tachyon-prod && cp env.example .env
# (edit .env with your keys + funded wallet)
solana-keygen new --outfile solver-solana.json --no-bip39-passphrase
solana-keygen new --outfile relayer-solana.json --no-bip39-passphrase
go run ./cmd/solana-key-to-base58 solver-solana.json   # paste into .env
go run ./cmd/solana-key-to-base58 relayer-solana.json  # paste into .env
solana airdrop 2 $(solana-keygen pubkey solver-solana.json) --url devnet
solana airdrop 2 $(solana-keygen pubkey relayer-solana.json) --url devnet
cd ../solana && \
  BRIDGE_INTENT_CHAIN_ID=40168 \
  BRIDGE_INTENT_USDC_MINT=CLQdHANA2Z2tjka21NqBkzoytaH18JtA2Q56Bt4tHPj \
  BRIDGE_INTENT_RELAYER_PUBKEY=$(solana-keygen pubkey ../tachyon-prod/relayer-solana.json) \
  ANCHOR_PROVIDER_URL=https://devnet.helius-rpc.com/?api-key=<YOUR> \
  anchor migrate --provider.cluster devnet

# Daily workflow:
cd tachyon-prod && go run ./cmd/relayer       # terminal 1
cd tachyon-prod && go run ./cmd/solver        # terminal 2
cd tachyon-prod/test/e2e && E2E_USE_STEALTH=1 go run base_solana_flow.go   # terminal 3
```

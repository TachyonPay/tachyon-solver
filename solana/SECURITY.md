# bridge_intent — Security Checklist Walkthrough

Walkthrough of the [solana-dev skill's security checklist](file:///Users/shreyaspadmakiran/.claude/skills/solana-dev/security.md)
items against the current `bridge_intent` Anchor program.

---

## 1. Signer authorization

Every state-changing instruction declares an explicit `Signer<'info>`
for the actor whose authorization the program enforces. Audit:

| Instruction | Required signer | Verified by |
|---|---|---|
| `initialize` | `payer` (becomes owner) | `Signer<'info>` |
| `add_relayer` | `owner` | `has_one = owner` constraint on `Config` |
| `remove_relayer` | `owner` | `has_one = owner` constraint on `Config` |
| `set_allowed_token_b_mint` | `owner` | `has_one = owner` constraint on `Config` |
| `create_intent` | `user` | `Signer<'info>` |
| `place_bid` | `solver` | `Signer<'info>` |
| `finalize_auction` | anyone | permissionless by design — winner is determined by stored bids, not caller |
| `deposit_and_pickup` | winning solver | `constraint = intent_record.winning_solver == solver.key()` |
| `settle_intent_with_chain2_verification` | authorized relayer | `relayer_auth` PDA must exist with `relayer == relayer.key()` |
| `cancel_intent` | user OR owner (after timeout) | runtime check on `caller.key()` |
| `solve_intent_on_chain2` | any solver | permissionless — recipients are explicitly listed in args |
| `register_stealth` | user | `Signer<'info>` — user pays rent and binds their pubkey to the PDA |
| `sweep_stealth` | originally-registered user | `constraint = stealth_registration.user == user.key()` |

No instruction relies on the absence of `Signer<'info>` or "the runtime
will require it" — every signer is statically declared.

## 2. Account ownership / discriminator validation

Anchor's `#[account]` macro auto-validates the discriminator and program
ownership on every typed account passed in. We use typed accounts for
every PDA: `Config`, `EscrowAuthority`, `RelayerAuth`, `IntentRecord`,
`IntentBids`, `SolvedRecord`, `StealthRegistration`. Forged accounts
fail the discriminator check before any handler logic runs.

The single place we read raw account bytes is the recipient ATA list in
`solve_intent_on_chain2`'s `remaining_accounts`. There we explicitly
parse the SPL token account's mint field (bytes 0..32) and require it
matches the asserted `token_b_mint`. See
[solve_intent_on_chain2.rs:91-101](programs/bridge_intent/src/instructions/solve_intent_on_chain2.rs).
A non-token-account PDA cannot pass: `TokenAccount::LEN` is a constant
the program reads against, and the mint check rejects mismatched mints.

## 3. PDA seed validation

Every PDA-receiving instruction declares the PDA via the `seeds = [...]`
constraint with `bump = state.bump` derived from the canonical state.
Bump values are stored in their host accounts at init and read on
re-derivation, never accepted from an arg:

```rust
#[account(seeds = [INTENT_SEED, &intent_id.to_le_bytes()], bump = intent_record.bump)]
pub intent_record: Account<'info, IntentRecord>,
```

This makes "PDA grinding attacks" (caller substitutes a different bump
to land at a different valid PDA) impossible — Anchor reverts if the
re-derivation doesn't match.

## 4. Token program variant — explicit Token-2022 rejection

The program imports `anchor_spl::token` (classic SPL Token program), not
`anchor_spl::token_interface` (which would accept both). All `Mint` and
`TokenAccount` typed accounts validate that their owner is the classic
SPL Token program (`Tokenkeg...`). Token-2022 mints fail Anchor's
`Mint::try_deserialize` with `AccountOwnerMismatch`.

Implication: token-2022 features (transfer hooks, confidential
transfers, interest-bearing tokens, etc.) cannot be used with this
bridge. This is intentional — those extensions can produce surprising
balance changes the bridge's accounting doesn't model.

The allowlist check in `solve_intent_on_chain2` (line 65–70) further
restricts accepted destination mints to two operator-curated mints
(USDC + WSOL by default).

## 5. Re-entrancy / CPI loop safety

Solana doesn't have synchronous re-entrancy in the EVM sense — all
state mutations land at the end of a CPI. The risk is an inner
instruction modifying an account our handler relies on after we've
already read it.

The only loop with a CPI inside it is `solve_intent_on_chain2`:

```rust
for (i, recipient_ata) in recipient_atas.iter().enumerate() {
    // ... mint check ...
    let ix = anchor_spl::token::spl_token::instruction::transfer(...);
    invoke(&ix, &[...])?;
}
```

Each inner CPI is a plain SPL `Transfer` from solver→recipient, signed
by the solver. There is no PDA-signing inside this loop — the solver
authorizes the transfer of their own tokens. The accounting state we
write to (`solved_record`) is created by the `init` constraint **before**
the loop runs and is updated only after the loop, in a single field
write. No re-entrancy surface.

Settlement (`settle_intent_with_chain2_verification`) does invoke a
PDA-signed CPI but only once, with no loop:

```rust
let signer_seeds: &[&[&[u8]]] = &[&[ESCROW_AUTH_SEED, &[escrow_bump]]];
let cpi_ctx = CpiContext::new_with_signer(...);
token::transfer(cpi_ctx, total)?;
```

## 6. Integer overflow

`#[profile.release.overflow-checks = true]` is set in
[solana/Cargo.toml](Cargo.toml). All arithmetic on user-supplied or
on-chain-stored amounts uses `checked_add` / `checked_mul`. Spot-check:

- [create_intent.rs:80](programs/bridge_intent/src/instructions/create_intent.rs#L80) — `amount_a + reward` checked
- [solve_intent_on_chain2.rs:74](programs/bridge_intent/src/instructions/solve_intent_on_chain2.rs#L74) — sum of recipient amounts checked, capped at `expected_amount_b`
- [settle_intent.rs:66-69](programs/bridge_intent/src/instructions/settle_intent.rs#L66) — `amount_a + reward + winning_bid` checked
- [cancel_intent.rs:90-94](programs/bridge_intent/src/instructions/cancel_intent.rs#L90) — `end_time + grace` checked, then `amount_a + reward` checked
- [place_bid.rs](programs/bridge_intent/src/instructions/place_bid.rs) — only comparisons, no arithmetic on bid amounts.

## 7. Replay / double-spend prevention

| Attack vector | Defense |
|---|---|
| Replay `solve_intent_on_chain2` with same (srcChainId, intentId) | `init` on `solved_record` PDA — second call fails with `account already in use` |
| Replay `register_stealth` for same (user, salt) | `init` on `stealth_registration` PDA — same |
| Bid below previous own bid | runtime constraint `bid_amount > existing.bid_amount` |
| Deposit twice for same intent | constraint `!intent_record.deposited` checked before mutating |
| Settle a non-deposited intent | constraint `intent_record.deposited` enforced |
| Settle a completed intent | constraint `!intent_record.completed` enforced |
| Cancel before grace period | runtime check `now > end_time + CANCEL_GRACE_PERIOD` |
| Sweep someone else's stealth | constraint `stealth_registration.user == user.key()` |

## 8. Authority delegation

The Config PDA stores a single `owner: Pubkey` controlling:

- Adding/removing relayer auth PDAs
- Updating the token-B mint allowlist

There is no `pending_owner` two-step transfer; this is acceptable for an
operator-controlled program. If multi-sig governance is desired for
mainnet, a Squads V4 vault can hold the Config owner key — the program
treats it as any other ed25519 signer.

The escrow authority PDA holds no pubkey field; it derives from
`[b"escrow"]` and only the program signs as it via `invoke_signed`.

## 9. Rent / DoS surface

Per-intent storage:

| Account | Size | Cost (lamports) | Paid by |
|---|---|---|---|
| `IntentRecord` | ~227 bytes | ~0.0019 SOL | user (`create_intent`) |
| `IntentBids` (max) | 8 + 4 + 16·48 + 1 = ~782 bytes | ~0.0064 SOL | user (`create_intent`) |
| `SolvedRecord` | ~105 bytes | ~0.00088 SOL | solver (`solve_intent_on_chain2`) |
| `StealthRegistration` | ~73 bytes | ~0.00061 SOL | user (`register_stealth`) |
| `RelayerAuth` | ~41 bytes | ~0.00034 SOL | owner |

Worst-case attack: an adversary creates `N` intents to exhaust
their own SOL balance. They can't push rent burden onto the program or
other users — Anchor `init` enforces `payer` is the signer. `cancel_intent`
returns rent of the IntentBids/IntentRecord accounts to the user via
`close = user` (currently NOT wired — Phase 4 cleanup). Today the rent
stays locked on cancel, which is a UX issue, not a security one.

## 10. Bump cap on bidders

`MAX_BIDDERS = 16` in [state.rs:5](programs/bridge_intent/src/state.rs#L5).
A bidder cap prevents an adversary from spamming bids to bloat the
`IntentBids` account beyond its preallocated size and DoS the auction.
The 17th bid attempt fails with `BidderCapReached`. Existing bidders
can update their own bid in-place without consuming new slots.

## 11. Cross-chain composite key

The on-chain `SolvedRecord` PDA seeds use
`[b"solved", src_chain_id_le, src_intent_id_le]`. This means an EVM
intent ID 42 from Horizen (chain 26514) and an EVM intent ID 42 from
Base (chain 8453) produce different solved-record PDAs — no collision.
The same convention is used by the EVM contract's `solvedIntents`
mapping (`keccak256(srcChainId, intentId)`).

The Solana chain ID we record is the LayerZero EID (30168/40168), not
a synthetic value, so cross-chain references in either direction match.

## 12. Frontend-side cautions

The FE team owns:

- Validating that user-input destination addresses are valid base58
  (Solana) or `0x`-hex (EVM); the contract enforces non-zero but does
  not parse format.
- Hard-rejecting Token-2022 mints in the token picker (the contract
  reverts but a clear UX message is friendlier).
- Treating the relayer-issued `sealed.ciphertext` as a one-shot value —
  losing it = losing access to funds delivered to the corresponding
  stealth PDA.
- u64 amounts as strings on the wire; never parse them as JS numbers.

Backend-side cautions are listed inline above; this doc is the single
source of truth for Solana security posture as of Phase 3.

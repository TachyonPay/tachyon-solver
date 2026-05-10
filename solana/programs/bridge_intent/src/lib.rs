//! bridge_intent — Solana-side BridgeIntent program for Tachyon.
//!
//! Mirrors the EVM `BridgeIntent.sol` lifecycle for both source-chain and
//! destination-chain roles:
//!
//!   source role  : create_intent → place_bid → finalize_auction →
//!                  deposit_and_pickup → settle_intent_with_chain2_verification
//!   dest role    : solve_intent_on_chain2
//!
//! Funds for *all* active source-side intents live in a single global escrow
//! ATA (one per SPL mint), authorized by the program-owned `EscrowAuthority`
//! PDA. Per-intent state lives in a small `IntentRecord` PDA. This avoids
//! per-intent ATA rent.
//!
//! Stealth addresses are PDAs derived from
//! `[b"stealth", user_pubkey, ephemeral_salt]`. The associated token account
//! at `ATA(stealth_pda, mint)` receives funds. Only the originally registered
//! `user_pubkey` can sweep them out.

use anchor_lang::prelude::*;

pub mod errors;
pub mod events;
pub mod instructions;
pub mod state;

use instructions::*;

declare_id!("64SZByfhWxvxu7haf153RHBvReNUFqz154RNagBbeYwk");

#[program]
pub mod bridge_intent {
    use super::*;

    pub fn initialize(
        ctx: Context<Initialize>,
        chain_id: u64,
        allowed_token_b_mints: [Pubkey; 2],
    ) -> Result<()> {
        instructions::initialize::handler(ctx, chain_id, allowed_token_b_mints)
    }

    pub fn add_relayer(ctx: Context<AddRelayer>) -> Result<()> {
        instructions::relayer_admin::add_handler(ctx)
    }

    pub fn remove_relayer(ctx: Context<RemoveRelayer>) -> Result<()> {
        instructions::relayer_admin::remove_handler(ctx)
    }

    pub fn set_allowed_token_b_mint(
        ctx: Context<SetAllowedTokenBMint>,
        index: u8,
        mint: Pubkey,
    ) -> Result<()> {
        instructions::relayer_admin::set_allowed_mint(ctx, index, mint)
    }

    pub fn create_intent(
        ctx: Context<CreateIntent>,
        amount_a: u64,
        expected_amount_b: u64,
        reward: u64,
        dest_chain_id: u64,
        recipient: [u8; 32],
        auction_duration: u64,
    ) -> Result<()> {
        instructions::create_intent::handler(
            ctx,
            amount_a,
            expected_amount_b,
            reward,
            dest_chain_id,
            recipient,
            auction_duration,
        )
    }

    pub fn place_bid(ctx: Context<PlaceBid>, intent_id: u64, bid_amount: u64) -> Result<()> {
        instructions::place_bid::handler(ctx, intent_id, bid_amount)
    }

    pub fn finalize_auction(ctx: Context<FinalizeAuction>, intent_id: u64) -> Result<()> {
        instructions::finalize_auction::handler(ctx, intent_id)
    }

    pub fn deposit_and_pickup(ctx: Context<DepositAndPickup>, intent_id: u64) -> Result<()> {
        instructions::deposit_and_pickup::handler(ctx, intent_id)
    }

    pub fn settle_intent_with_chain2_verification(
        ctx: Context<SettleIntent>,
        intent_id: u64,
        chain2_intent_id: u64,
    ) -> Result<()> {
        instructions::settle_intent::handler(ctx, intent_id, chain2_intent_id)
    }

    pub fn cancel_intent(ctx: Context<CancelIntent>, intent_id: u64) -> Result<()> {
        instructions::cancel_intent::handler(ctx, intent_id)
    }

    pub fn solve_intent_on_chain2<'info>(
        ctx: Context<'_, '_, '_, 'info, SolveIntentOnChain2<'info>>,
        src_chain_id: u64,
        src_intent_id: u64,
        user: [u8; 32],
        expected_amount_b: u64,
        amounts: Vec<u64>,
    ) -> Result<()> {
        instructions::solve_intent_on_chain2::handler(
            ctx,
            src_chain_id,
            src_intent_id,
            user,
            expected_amount_b,
            amounts,
        )
    }

    pub fn register_stealth(
        ctx: Context<RegisterStealth>,
        ephemeral_salt: [u8; 32],
    ) -> Result<()> {
        instructions::register_stealth::handler(ctx, ephemeral_salt)
    }

    pub fn sweep_stealth(ctx: Context<SweepStealth>, ephemeral_salt: [u8; 32]) -> Result<()> {
        instructions::sweep_stealth::handler(ctx, ephemeral_salt)
    }
}

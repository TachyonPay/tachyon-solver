use crate::errors::BridgeIntentError;
use crate::events::IntentCreated;
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

#[derive(Accounts)]
pub struct CreateIntent<'info> {
    #[account(mut)]
    pub user: Signer<'info>,

    #[account(
        mut,
        seeds = [CONFIG_SEED],
        bump = config.bump,
    )]
    pub config: Account<'info, Config>,

    pub token_a_mint: Account<'info, Mint>,

    /// Token-B mint is recorded so the eventual destination delivery can be
    /// verified. For non-Solana destinations, callers SHOULD pass the
    /// `token_a_mint` here as a placeholder (Solana stores 32-byte pubkeys
    /// natively; the canonical destination token address travels in the
    /// off-chain message).
    pub token_b_mint: Account<'info, Mint>,

    #[account(
        mut,
        token::mint = token_a_mint,
        token::authority = user,
    )]
    pub user_token_a_ata: Account<'info, TokenAccount>,

    #[account(
        seeds = [ESCROW_AUTH_SEED],
        bump = config.escrow_authority_bump,
    )]
    pub escrow_authority: Account<'info, EscrowAuthority>,

    #[account(
        init_if_needed,
        payer = user,
        associated_token::mint = token_a_mint,
        associated_token::authority = escrow_authority,
    )]
    pub escrow_token_a_ata: Account<'info, TokenAccount>,

    #[account(
        init,
        payer = user,
        space = IntentRecord::SIZE,
        seeds = [INTENT_SEED, &config.next_intent_id.to_le_bytes()],
        bump,
    )]
    pub intent_record: Account<'info, IntentRecord>,

    #[account(
        init,
        payer = user,
        space = IntentBids::SIZE,
        seeds = [BIDS_SEED, &config.next_intent_id.to_le_bytes()],
        bump,
    )]
    pub intent_bids: Account<'info, IntentBids>,

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
    pub system_program: Program<'info, System>,
    pub rent: Sysvar<'info, Rent>,
}

pub fn handler(
    ctx: Context<CreateIntent>,
    amount_a: u64,
    expected_amount_b: u64,
    reward: u64,
    dest_chain_id: u64,
    recipient: [u8; 32],
    auction_duration: u64,
) -> Result<()> {
    require!(amount_a > 0, BridgeIntentError::InvalidAmount);
    require!(expected_amount_b > 0, BridgeIntentError::InvalidAmount);
    require!(reward > 0, BridgeIntentError::InvalidReward);
    require!(dest_chain_id > 0, BridgeIntentError::InvalidDestChainId);
    require!(recipient != [0u8; 32], BridgeIntentError::InvalidRecipient);
    let duration_i64 = auction_duration as i64;
    require!(
        duration_i64 >= MIN_AUCTION_DURATION && duration_i64 <= MAX_AUCTION_DURATION,
        BridgeIntentError::InvalidAuctionDuration
    );

    let now = Clock::get()?.unix_timestamp;
    let end_time = now
        .checked_add(duration_i64)
        .ok_or(BridgeIntentError::Overflow)?;

    let total_in = amount_a
        .checked_add(reward)
        .ok_or(BridgeIntentError::Overflow)?;

    let cpi_ctx = CpiContext::new(
        ctx.accounts.token_program.to_account_info(),
        Transfer {
            from: ctx.accounts.user_token_a_ata.to_account_info(),
            to: ctx.accounts.escrow_token_a_ata.to_account_info(),
            authority: ctx.accounts.user.to_account_info(),
        },
    );
    token::transfer(cpi_ctx, total_in)?;

    let intent_id = ctx.accounts.config.next_intent_id;
    ctx.accounts.config.next_intent_id = intent_id
        .checked_add(1)
        .ok_or(BridgeIntentError::Overflow)?;

    let record = &mut ctx.accounts.intent_record;
    record.intent_id = intent_id;
    record.user = ctx.accounts.user.key();
    record.token_a_mint = ctx.accounts.token_a_mint.key();
    record.token_b_mint = ctx.accounts.token_b_mint.key();
    record.amount_a = amount_a;
    record.expected_amount_b = expected_amount_b;
    record.reward = reward;
    record.dest_chain_id = dest_chain_id;
    record.recipient = recipient;
    record.end_time = end_time;
    record.winning_solver = Pubkey::default();
    record.winning_bid = 0;
    record.completed = false;
    record.deposited = false;
    record.bump = ctx.bumps.intent_record;

    let bids = &mut ctx.accounts.intent_bids;
    bids.intent_id = intent_id;
    bids.bids = Vec::with_capacity(MAX_BIDDERS);
    bids.bump = ctx.bumps.intent_bids;

    emit!(IntentCreated {
        intent_id,
        user: record.user,
        token_a_mint: record.token_a_mint,
        token_b_mint: record.token_b_mint,
        amount_a,
        expected_amount_b,
        reward,
        dest_chain_id,
        recipient,
        end_time,
    });

    Ok(())
}

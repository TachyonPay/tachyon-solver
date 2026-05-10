use crate::errors::BridgeIntentError;
use crate::events::IntentCancelled;
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

#[derive(Accounts)]
#[instruction(intent_id: u64)]
pub struct CancelIntent<'info> {
    #[account(mut)]
    pub caller: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
    )]
    pub config: Account<'info, Config>,

    #[account(
        mut,
        seeds = [INTENT_SEED, &intent_id.to_le_bytes()],
        bump = intent_record.bump,
        constraint = !intent_record.completed @ BridgeIntentError::IntentAlreadyCompleted,
    )]
    pub intent_record: Account<'info, IntentRecord>,

    #[account(
        constraint = token_a_mint.key() == intent_record.token_a_mint @ BridgeIntentError::AccountMintMismatch,
    )]
    pub token_a_mint: Account<'info, Mint>,

    #[account(
        seeds = [ESCROW_AUTH_SEED],
        bump = config.escrow_authority_bump,
    )]
    pub escrow_authority: Account<'info, EscrowAuthority>,

    #[account(
        mut,
        associated_token::mint = token_a_mint,
        associated_token::authority = escrow_authority,
    )]
    pub escrow_token_a_ata: Account<'info, TokenAccount>,

    /// CHECK: pubkey-only validation; must match `intent_record.user`.
    #[account(
        constraint = user.key() == intent_record.user @ BridgeIntentError::NotAuthorized,
    )]
    pub user: UncheckedAccount<'info>,

    #[account(
        mut,
        token::mint = token_a_mint,
        token::authority = user,
    )]
    pub user_token_a_ata: Account<'info, TokenAccount>,

    /// CHECK: optional — must match `intent_record.winning_solver`. Pass user
    /// account here if no solver was selected (passes the constraint as a no-op
    /// because winning_bid = 0 will skip the refund).
    #[account(
        constraint = winning_solver.key() == intent_record.winning_solver
            || intent_record.winning_solver == Pubkey::default() @ BridgeIntentError::NotWinningSolver,
    )]
    pub winning_solver: UncheckedAccount<'info>,

    #[account(
        mut,
        token::mint = token_a_mint,
    )]
    pub winning_solver_ata: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
}

pub fn handler(ctx: Context<CancelIntent>, _intent_id: u64) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let cancel_after = ctx
        .accounts
        .intent_record
        .end_time
        .checked_add(CANCEL_GRACE_PERIOD)
        .ok_or(BridgeIntentError::Overflow)?;
    require!(now > cancel_after, BridgeIntentError::TooEarlyToCancel);

    let caller = ctx.accounts.caller.key();
    let is_user = caller == ctx.accounts.intent_record.user;
    let is_owner = caller == ctx.accounts.config.owner;
    require!(is_user || is_owner, BridgeIntentError::NotAuthorized);

    let escrow_bump = ctx.accounts.config.escrow_authority_bump;
    let signer_seeds: &[&[&[u8]]] = &[&[ESCROW_AUTH_SEED, &[escrow_bump]]];

    let user_refund = ctx
        .accounts
        .intent_record
        .amount_a
        .checked_add(ctx.accounts.intent_record.reward)
        .ok_or(BridgeIntentError::Overflow)?;

    let cpi_user = CpiContext::new_with_signer(
        ctx.accounts.token_program.to_account_info(),
        Transfer {
            from: ctx.accounts.escrow_token_a_ata.to_account_info(),
            to: ctx.accounts.user_token_a_ata.to_account_info(),
            authority: ctx.accounts.escrow_authority.to_account_info(),
        },
        signer_seeds,
    );
    token::transfer(cpi_user, user_refund)?;

    if ctx.accounts.intent_record.deposited {
        let cpi_solver = CpiContext::new_with_signer(
            ctx.accounts.token_program.to_account_info(),
            Transfer {
                from: ctx.accounts.escrow_token_a_ata.to_account_info(),
                to: ctx.accounts.winning_solver_ata.to_account_info(),
                authority: ctx.accounts.escrow_authority.to_account_info(),
            },
            signer_seeds,
        );
        token::transfer(cpi_solver, ctx.accounts.intent_record.winning_bid)?;
    }

    ctx.accounts.intent_record.completed = true;

    emit!(IntentCancelled {
        intent_id: ctx.accounts.intent_record.intent_id,
        user: ctx.accounts.intent_record.user,
    });

    Ok(())
}

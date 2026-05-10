use crate::errors::BridgeIntentError;
use crate::events::SolverDeposited;
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

#[derive(Accounts)]
#[instruction(intent_id: u64)]
pub struct DepositAndPickup<'info> {
    #[account(mut)]
    pub solver: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
    )]
    pub config: Account<'info, Config>,

    #[account(
        mut,
        seeds = [INTENT_SEED, &intent_id.to_le_bytes()],
        bump = intent_record.bump,
        constraint = intent_record.winning_solver == solver.key() @ BridgeIntentError::NotWinningSolver,
        constraint = !intent_record.completed @ BridgeIntentError::IntentAlreadyCompleted,
        constraint = !intent_record.deposited @ BridgeIntentError::AlreadyDeposited,
    )]
    pub intent_record: Account<'info, IntentRecord>,

    #[account(
        constraint = token_a_mint.key() == intent_record.token_a_mint @ BridgeIntentError::AccountMintMismatch,
    )]
    pub token_a_mint: Account<'info, Mint>,

    #[account(
        mut,
        token::mint = token_a_mint,
        token::authority = solver,
    )]
    pub solver_token_a_ata: Account<'info, TokenAccount>,

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

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
}

pub fn handler(ctx: Context<DepositAndPickup>, _intent_id: u64) -> Result<()> {
    let bid = ctx.accounts.intent_record.winning_bid;
    require!(bid > 0, BridgeIntentError::BidZero);

    let cpi_ctx = CpiContext::new(
        ctx.accounts.token_program.to_account_info(),
        Transfer {
            from: ctx.accounts.solver_token_a_ata.to_account_info(),
            to: ctx.accounts.escrow_token_a_ata.to_account_info(),
            authority: ctx.accounts.solver.to_account_info(),
        },
    );
    token::transfer(cpi_ctx, bid)?;

    ctx.accounts.intent_record.deposited = true;

    emit!(SolverDeposited {
        intent_id: ctx.accounts.intent_record.intent_id,
        solver: ctx.accounts.solver.key(),
        amount: bid,
    });

    Ok(())
}

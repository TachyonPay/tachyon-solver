use crate::errors::BridgeIntentError;
use crate::events::{IntentCompleted, IntentSettled};
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

#[derive(Accounts)]
#[instruction(intent_id: u64)]
pub struct SettleIntent<'info> {
    #[account(mut)]
    pub relayer: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
    )]
    pub config: Account<'info, Config>,

    #[account(
        seeds = [RELAYER_SEED, relayer.key().as_ref()],
        bump = relayer_auth.bump,
        constraint = relayer_auth.relayer == relayer.key() @ BridgeIntentError::NotAuthorized,
    )]
    pub relayer_auth: Account<'info, RelayerAuth>,

    #[account(
        mut,
        seeds = [INTENT_SEED, &intent_id.to_le_bytes()],
        bump = intent_record.bump,
        constraint = intent_record.deposited @ BridgeIntentError::NotDeposited,
        constraint = !intent_record.completed @ BridgeIntentError::IntentAlreadyCompleted,
        constraint = intent_record.winning_solver != Pubkey::default() @ BridgeIntentError::NoBids,
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

    /// Solver receiving the released funds. Must match `intent_record.winning_solver`.
    /// CHECK: pubkey-only validation; balances are held in `winning_solver_ata`.
    #[account(
        constraint = winning_solver.key() == intent_record.winning_solver @ BridgeIntentError::NotWinningSolver,
    )]
    pub winning_solver: UncheckedAccount<'info>,

    #[account(
        mut,
        token::mint = token_a_mint,
        token::authority = winning_solver,
    )]
    pub winning_solver_ata: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
}

pub fn handler(
    ctx: Context<SettleIntent>,
    _intent_id: u64,
    chain2_intent_id: u64,
) -> Result<()> {
    let total = ctx
        .accounts
        .intent_record
        .amount_a
        .checked_add(ctx.accounts.intent_record.reward)
        .and_then(|v| v.checked_add(ctx.accounts.intent_record.winning_bid))
        .ok_or(BridgeIntentError::Overflow)?;

    let escrow_bump = ctx.accounts.config.escrow_authority_bump;
    let signer_seeds: &[&[&[u8]]] = &[&[ESCROW_AUTH_SEED, &[escrow_bump]]];

    let cpi_ctx = CpiContext::new_with_signer(
        ctx.accounts.token_program.to_account_info(),
        Transfer {
            from: ctx.accounts.escrow_token_a_ata.to_account_info(),
            to: ctx.accounts.winning_solver_ata.to_account_info(),
            authority: ctx.accounts.escrow_authority.to_account_info(),
        },
        signer_seeds,
    );
    token::transfer(cpi_ctx, total)?;

    let record = &mut ctx.accounts.intent_record;
    record.completed = true;

    emit!(IntentSettled {
        intent_id: record.intent_id,
        solver: record.winning_solver,
        amount_a: record.amount_a,
        reward: record.reward,
        winning_bid: record.winning_bid,
        chain2_intent_id,
    });
    emit!(IntentCompleted {
        intent_id: record.intent_id,
        solver: record.winning_solver,
    });

    Ok(())
}

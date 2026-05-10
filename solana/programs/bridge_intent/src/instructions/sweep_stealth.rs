use crate::errors::BridgeIntentError;
use crate::events::StealthSwept;
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

#[derive(Accounts)]
#[instruction(ephemeral_salt: [u8; 32])]
pub struct SweepStealth<'info> {
    #[account(mut)]
    pub user: Signer<'info>,

    #[account(
        seeds = [STEALTH_SEED, user.key().as_ref(), &ephemeral_salt],
        bump = stealth_registration.bump,
        constraint = stealth_registration.user == user.key() @ BridgeIntentError::StealthRegistrationMismatch,
    )]
    pub stealth_registration: Account<'info, StealthRegistration>,

    pub mint: Account<'info, Mint>,

    #[account(
        mut,
        associated_token::mint = mint,
        associated_token::authority = stealth_registration,
    )]
    pub stealth_ata: Account<'info, TokenAccount>,

    #[account(
        mut,
        token::mint = mint,
        token::authority = user,
    )]
    pub user_ata: Account<'info, TokenAccount>,

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
}

pub fn handler(ctx: Context<SweepStealth>, ephemeral_salt: [u8; 32]) -> Result<()> {
    let amount = ctx.accounts.stealth_ata.amount;
    require!(amount > 0, BridgeIntentError::InvalidAmount);

    let user_key = ctx.accounts.user.key();
    let bump = ctx.accounts.stealth_registration.bump;
    let signer_seeds: &[&[&[u8]]] = &[&[
        STEALTH_SEED,
        user_key.as_ref(),
        &ephemeral_salt,
        &[bump],
    ]];

    let cpi_ctx = CpiContext::new_with_signer(
        ctx.accounts.token_program.to_account_info(),
        Transfer {
            from: ctx.accounts.stealth_ata.to_account_info(),
            to: ctx.accounts.user_ata.to_account_info(),
            authority: ctx.accounts.stealth_registration.to_account_info(),
        },
        signer_seeds,
    );
    token::transfer(cpi_ctx, amount)?;

    emit!(StealthSwept {
        user: user_key,
        stealth_pda: ctx.accounts.stealth_registration.key(),
        mint: ctx.accounts.mint.key(),
        amount,
    });

    Ok(())
}

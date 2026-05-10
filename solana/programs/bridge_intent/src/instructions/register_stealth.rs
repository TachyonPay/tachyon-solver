use crate::events::StealthRegistered;
use crate::state::*;
use anchor_lang::prelude::*;

#[derive(Accounts)]
#[instruction(ephemeral_salt: [u8; 32])]
pub struct RegisterStealth<'info> {
    #[account(mut)]
    pub user: Signer<'info>,

    #[account(
        init,
        payer = user,
        space = StealthRegistration::SIZE,
        seeds = [STEALTH_SEED, user.key().as_ref(), &ephemeral_salt],
        bump,
    )]
    pub stealth_registration: Account<'info, StealthRegistration>,

    pub system_program: Program<'info, System>,
}

pub fn handler(ctx: Context<RegisterStealth>, ephemeral_salt: [u8; 32]) -> Result<()> {
    let reg = &mut ctx.accounts.stealth_registration;
    reg.user = ctx.accounts.user.key();
    reg.ephemeral_salt = ephemeral_salt;
    reg.created_at = Clock::get()?.unix_timestamp;
    reg.bump = ctx.bumps.stealth_registration;

    emit!(StealthRegistered {
        user: reg.user,
        stealth_pda: reg.key(),
        ephemeral_salt,
    });

    Ok(())
}

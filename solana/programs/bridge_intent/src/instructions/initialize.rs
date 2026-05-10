use crate::state::*;
use anchor_lang::prelude::*;

#[derive(Accounts)]
pub struct Initialize<'info> {
    #[account(mut)]
    pub payer: Signer<'info>,

    #[account(
        init,
        payer = payer,
        space = Config::SIZE,
        seeds = [CONFIG_SEED],
        bump,
    )]
    pub config: Account<'info, Config>,

    #[account(
        init,
        payer = payer,
        space = EscrowAuthority::SIZE,
        seeds = [ESCROW_AUTH_SEED],
        bump,
    )]
    pub escrow_authority: Account<'info, EscrowAuthority>,

    pub system_program: Program<'info, System>,
}

pub fn handler(
    ctx: Context<Initialize>,
    chain_id: u64,
    allowed_token_b_mints: [Pubkey; 2],
) -> Result<()> {
    let config = &mut ctx.accounts.config;
    config.owner = ctx.accounts.payer.key();
    config.chain_id = chain_id;
    config.allowed_token_b_mints = allowed_token_b_mints;
    config.next_intent_id = 1;
    config.escrow_authority_bump = ctx.bumps.escrow_authority;
    config.bump = ctx.bumps.config;

    let escrow = &mut ctx.accounts.escrow_authority;
    escrow.bump = ctx.bumps.escrow_authority;

    Ok(())
}

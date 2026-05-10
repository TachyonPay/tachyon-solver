use crate::errors::BridgeIntentError;
use crate::state::*;
use anchor_lang::prelude::*;

#[derive(Accounts)]
#[instruction()]
pub struct AddRelayer<'info> {
    #[account(mut)]
    pub owner: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
        has_one = owner @ BridgeIntentError::NotAuthorized,
    )]
    pub config: Account<'info, Config>,

    /// CHECK: pubkey of the relayer being authorized; not dereferenced.
    pub relayer: UncheckedAccount<'info>,

    #[account(
        init,
        payer = owner,
        space = RelayerAuth::SIZE,
        seeds = [RELAYER_SEED, relayer.key().as_ref()],
        bump,
    )]
    pub relayer_auth: Account<'info, RelayerAuth>,

    pub system_program: Program<'info, System>,
}

pub fn add_handler(ctx: Context<AddRelayer>) -> Result<()> {
    let auth = &mut ctx.accounts.relayer_auth;
    auth.relayer = ctx.accounts.relayer.key();
    auth.bump = ctx.bumps.relayer_auth;
    Ok(())
}

#[derive(Accounts)]
pub struct RemoveRelayer<'info> {
    #[account(mut)]
    pub owner: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
        has_one = owner @ BridgeIntentError::NotAuthorized,
    )]
    pub config: Account<'info, Config>,

    /// CHECK: pubkey of the relayer being deauthorized; not dereferenced.
    pub relayer: UncheckedAccount<'info>,

    #[account(
        mut,
        close = owner,
        seeds = [RELAYER_SEED, relayer.key().as_ref()],
        bump = relayer_auth.bump,
        has_one = relayer @ BridgeIntentError::NotAuthorized,
    )]
    pub relayer_auth: Account<'info, RelayerAuth>,
}

pub fn remove_handler(_ctx: Context<RemoveRelayer>) -> Result<()> {
    Ok(())
}

#[derive(Accounts)]
pub struct SetAllowedTokenBMint<'info> {
    pub owner: Signer<'info>,

    #[account(
        mut,
        seeds = [CONFIG_SEED],
        bump = config.bump,
        has_one = owner @ BridgeIntentError::NotAuthorized,
    )]
    pub config: Account<'info, Config>,
}

pub fn set_allowed_mint(
    ctx: Context<SetAllowedTokenBMint>,
    index: u8,
    mint: Pubkey,
) -> Result<()> {
    let config = &mut ctx.accounts.config;
    let idx = index as usize;
    require!(
        idx < config.allowed_token_b_mints.len(),
        BridgeIntentError::AllowlistIndexOutOfBounds
    );
    config.allowed_token_b_mints[idx] = mint;
    Ok(())
}

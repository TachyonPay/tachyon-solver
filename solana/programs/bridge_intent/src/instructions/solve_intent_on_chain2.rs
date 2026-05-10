use crate::errors::BridgeIntentError;
use crate::events::{IntentCompleted, IntentSolvedOnChain2};
use crate::state::*;
use anchor_lang::prelude::*;
use anchor_lang::solana_program::program::invoke;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token::{self, Mint, Token, TokenAccount, Transfer};

/// Destination-chain delivery instruction.
///
/// `recipient` ATAs are passed via `remaining_accounts`. They must already
/// exist — the solver should prepend
/// `create_associated_token_account_idempotent` instructions to the same
/// transaction. The solver pays the rent.
#[derive(Accounts)]
#[instruction(src_chain_id: u64, src_intent_id: u64)]
pub struct SolveIntentOnChain2<'info> {
    #[account(mut)]
    pub solver: Signer<'info>,

    #[account(
        seeds = [CONFIG_SEED],
        bump = config.bump,
    )]
    pub config: Account<'info, Config>,

    pub token_b_mint: Account<'info, Mint>,

    #[account(
        mut,
        token::mint = token_b_mint,
        token::authority = solver,
    )]
    pub solver_token_b_ata: Account<'info, TokenAccount>,

    #[account(
        init,
        payer = solver,
        space = SolvedRecord::SIZE,
        seeds = [
            SOLVED_SEED,
            &src_chain_id.to_le_bytes(),
            &src_intent_id.to_le_bytes(),
        ],
        bump,
    )]
    pub solved_record: Account<'info, SolvedRecord>,

    pub token_program: Program<'info, Token>,
    pub associated_token_program: Program<'info, AssociatedToken>,
    pub system_program: Program<'info, System>,
}

pub fn handler<'info>(
    ctx: Context<'_, '_, '_, 'info, SolveIntentOnChain2<'info>>,
    src_chain_id: u64,
    src_intent_id: u64,
    _user: [u8; 32],
    expected_amount_b: u64,
    amounts: Vec<u64>,
) -> Result<()> {
    require!(src_chain_id > 0, BridgeIntentError::InvalidDestChainId);
    require!(src_intent_id > 0, BridgeIntentError::InvalidAmount);
    require!(expected_amount_b > 0, BridgeIntentError::InvalidAmount);
    require!(!amounts.is_empty(), BridgeIntentError::EmptyRecipients);

    let mint_key = ctx.accounts.token_b_mint.key();
    let allowed = ctx
        .accounts
        .config
        .allowed_token_b_mints
        .iter()
        .any(|m| *m == mint_key);
    require!(allowed, BridgeIntentError::TokenMintNotAllowed);

    let recipient_atas = ctx.remaining_accounts;
    require!(
        recipient_atas.len() == amounts.len(),
        BridgeIntentError::RecipientCountMismatch
    );

    let mut total: u64 = 0;
    for a in amounts.iter() {
        total = total.checked_add(*a).ok_or(BridgeIntentError::Overflow)?;
    }
    require!(
        total <= expected_amount_b,
        BridgeIntentError::TotalExceedsExpected
    );

    for (i, recipient_ata) in recipient_atas.iter().enumerate() {
        let amount = amounts[i];
        if amount == 0 {
            continue;
        }
        let ata_data = recipient_ata.try_borrow_data()?;
        require!(
            ata_data.len() >= TokenAccount::LEN,
            BridgeIntentError::AccountMintMismatch
        );
        let mint_in_ata = Pubkey::try_from(&ata_data[0..32])
            .map_err(|_| error!(BridgeIntentError::AccountMintMismatch))?;
        require!(
            mint_in_ata == mint_key,
            BridgeIntentError::AccountMintMismatch
        );
        drop(ata_data);

        let ix = anchor_spl::token::spl_token::instruction::transfer(
            &anchor_spl::token::ID,
            &ctx.accounts.solver_token_b_ata.key(),
            recipient_ata.key,
            &ctx.accounts.solver.key(),
            &[],
            amount,
        )?;
        invoke(
            &ix,
            &[
                ctx.accounts.solver_token_b_ata.to_account_info(),
                recipient_ata.clone(),
                ctx.accounts.solver.to_account_info(),
                ctx.accounts.token_program.to_account_info(),
            ],
        )?;
    }

    let solved = &mut ctx.accounts.solved_record;
    solved.src_chain_id = src_chain_id;
    solved.src_intent_id = src_intent_id;
    solved.solver = ctx.accounts.solver.key();
    solved.token_b_mint = mint_key;
    solved.total_amount = total;
    solved.solved_at = Clock::get()?.unix_timestamp;
    solved.bump = ctx.bumps.solved_record;

    emit!(IntentSolvedOnChain2 {
        src_chain_id,
        src_intent_id,
        solver: ctx.accounts.solver.key(),
        token_b_mint: mint_key,
        total_amount: total,
    });
    emit!(IntentCompleted {
        intent_id: src_intent_id,
        solver: ctx.accounts.solver.key(),
    });

    Ok(())
}

// Keep the unused import alive when std token::transfer is not directly used.
#[allow(dead_code)]
fn _silence() -> Option<Transfer<'static>> {
    let _ = token::ID;
    None
}

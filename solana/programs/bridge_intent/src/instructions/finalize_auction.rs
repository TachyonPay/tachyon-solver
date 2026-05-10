use crate::errors::BridgeIntentError;
use crate::events::IntentWon;
use crate::state::*;
use anchor_lang::prelude::*;

#[derive(Accounts)]
#[instruction(intent_id: u64)]
pub struct FinalizeAuction<'info> {
    pub caller: Signer<'info>,

    #[account(
        mut,
        seeds = [INTENT_SEED, &intent_id.to_le_bytes()],
        bump = intent_record.bump,
    )]
    pub intent_record: Account<'info, IntentRecord>,

    #[account(
        seeds = [BIDS_SEED, &intent_id.to_le_bytes()],
        bump = intent_bids.bump,
    )]
    pub intent_bids: Account<'info, IntentBids>,
}

pub fn handler(ctx: Context<FinalizeAuction>, _intent_id: u64) -> Result<()> {
    let record = &mut ctx.accounts.intent_record;
    require!(!record.completed, BridgeIntentError::IntentAlreadyCompleted);
    require!(
        record.winning_solver == Pubkey::default(),
        BridgeIntentError::AuctionAlreadyFinalized
    );

    let now = Clock::get()?.unix_timestamp;
    require!(now >= record.end_time, BridgeIntentError::AuctionStillActive);

    let bids = &ctx.accounts.intent_bids;
    require!(!bids.bids.is_empty(), BridgeIntentError::NoBids);

    let mut highest_bid: u64 = 0;
    let mut winner: Pubkey = Pubkey::default();
    for b in bids.bids.iter() {
        if b.bid_amount > highest_bid {
            highest_bid = b.bid_amount;
            winner = b.solver;
        }
    }

    record.winning_solver = winner;
    record.winning_bid = highest_bid;

    emit!(IntentWon {
        intent_id: record.intent_id,
        winning_solver: winner,
        winning_bid: highest_bid,
    });

    Ok(())
}

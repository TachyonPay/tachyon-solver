use crate::errors::BridgeIntentError;
use crate::events::BidPlaced;
use crate::state::*;
use anchor_lang::prelude::*;

#[derive(Accounts)]
#[instruction(intent_id: u64)]
pub struct PlaceBid<'info> {
    pub solver: Signer<'info>,

    #[account(
        seeds = [INTENT_SEED, &intent_id.to_le_bytes()],
        bump = intent_record.bump,
    )]
    pub intent_record: Account<'info, IntentRecord>,

    #[account(
        mut,
        seeds = [BIDS_SEED, &intent_id.to_le_bytes()],
        bump = intent_bids.bump,
    )]
    pub intent_bids: Account<'info, IntentBids>,
}

pub fn handler(ctx: Context<PlaceBid>, _intent_id: u64, bid_amount: u64) -> Result<()> {
    require!(bid_amount > 0, BridgeIntentError::BidZero);

    let record = &ctx.accounts.intent_record;
    require!(!record.completed, BridgeIntentError::IntentAlreadyCompleted);

    let now = Clock::get()?.unix_timestamp;
    require!(now < record.end_time, BridgeIntentError::AuctionEnded);

    let bids = &mut ctx.accounts.intent_bids;

    let solver_key = ctx.accounts.solver.key();
    let mut existing_idx: Option<usize> = None;
    for (i, b) in bids.bids.iter().enumerate() {
        if b.solver == solver_key {
            existing_idx = Some(i);
            break;
        }
    }

    if let Some(i) = existing_idx {
        require!(
            bid_amount > bids.bids[i].bid_amount,
            BridgeIntentError::BidNotHigher
        );
        bids.bids[i].bid_amount = bid_amount;
        bids.bids[i].timestamp = now;
    } else {
        require!(
            bids.bids.len() < MAX_BIDDERS,
            BridgeIntentError::BidderCapReached
        );
        bids.bids.push(Bid {
            solver: solver_key,
            bid_amount,
            timestamp: now,
        });
    }

    emit!(BidPlaced {
        intent_id: record.intent_id,
        solver: solver_key,
        bid_amount,
        timestamp: now,
    });

    Ok(())
}

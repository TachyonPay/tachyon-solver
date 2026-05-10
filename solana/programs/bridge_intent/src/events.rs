use anchor_lang::prelude::*;

#[event]
pub struct IntentCreated {
    pub intent_id: u64,
    pub user: Pubkey,
    pub token_a_mint: Pubkey,
    pub token_b_mint: Pubkey,
    pub amount_a: u64,
    pub expected_amount_b: u64,
    pub reward: u64,
    pub dest_chain_id: u64,
    pub recipient: [u8; 32],
    pub end_time: i64,
}

#[event]
pub struct BidPlaced {
    pub intent_id: u64,
    pub solver: Pubkey,
    pub bid_amount: u64,
    pub timestamp: i64,
}

#[event]
pub struct IntentWon {
    pub intent_id: u64,
    pub winning_solver: Pubkey,
    pub winning_bid: u64,
}

#[event]
pub struct SolverDeposited {
    pub intent_id: u64,
    pub solver: Pubkey,
    pub amount: u64,
}

#[event]
pub struct IntentSettled {
    pub intent_id: u64,
    pub solver: Pubkey,
    pub amount_a: u64,
    pub reward: u64,
    pub winning_bid: u64,
    pub chain2_intent_id: u64,
}

#[event]
pub struct IntentSolvedOnChain2 {
    pub src_chain_id: u64,
    pub src_intent_id: u64,
    pub solver: Pubkey,
    pub token_b_mint: Pubkey,
    pub total_amount: u64,
}

#[event]
pub struct IntentCompleted {
    pub intent_id: u64,
    pub solver: Pubkey,
}

#[event]
pub struct IntentCancelled {
    pub intent_id: u64,
    pub user: Pubkey,
}

#[event]
pub struct StealthRegistered {
    pub user: Pubkey,
    pub stealth_pda: Pubkey,
    pub ephemeral_salt: [u8; 32],
}

#[event]
pub struct StealthSwept {
    pub user: Pubkey,
    pub stealth_pda: Pubkey,
    pub mint: Pubkey,
    pub amount: u64,
}

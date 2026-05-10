use anchor_lang::prelude::*;

pub const MAX_BIDDERS: usize = 16;
pub const MIN_AUCTION_DURATION: i64 = 5;
pub const MAX_AUCTION_DURATION: i64 = 24 * 60 * 60;
pub const CANCEL_GRACE_PERIOD: i64 = 60 * 60;

pub const CONFIG_SEED: &[u8] = b"config";
pub const ESCROW_AUTH_SEED: &[u8] = b"escrow";
pub const INTENT_SEED: &[u8] = b"intent";
pub const BIDS_SEED: &[u8] = b"bids";
pub const SOLVED_SEED: &[u8] = b"solved";
pub const STEALTH_SEED: &[u8] = b"stealth";
pub const RELAYER_SEED: &[u8] = b"relayer";

#[account]
pub struct Config {
    pub owner: Pubkey,
    pub chain_id: u64,
    pub allowed_token_b_mints: [Pubkey; 2],
    pub next_intent_id: u64,
    pub escrow_authority_bump: u8,
    pub bump: u8,
}

impl Config {
    pub const SIZE: usize = 8 + 32 + 8 + (32 * 2) + 8 + 1 + 1;
}

#[account]
pub struct EscrowAuthority {
    pub bump: u8,
}

impl EscrowAuthority {
    pub const SIZE: usize = 8 + 1;
}

#[account]
pub struct RelayerAuth {
    pub relayer: Pubkey,
    pub bump: u8,
}

impl RelayerAuth {
    pub const SIZE: usize = 8 + 32 + 1;
}

#[account]
pub struct IntentRecord {
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
    pub winning_solver: Pubkey,
    pub winning_bid: u64,
    pub completed: bool,
    pub deposited: bool,
    pub bump: u8,
}

impl IntentRecord {
    pub const SIZE: usize = 8 + 8 + 32 + 32 + 32 + 8 + 8 + 8 + 8 + 32 + 8 + 32 + 8 + 1 + 1 + 1;
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy)]
pub struct Bid {
    pub solver: Pubkey,
    pub bid_amount: u64,
    pub timestamp: i64,
}

impl Bid {
    pub const SIZE: usize = 32 + 8 + 8;
}

#[account]
pub struct IntentBids {
    pub intent_id: u64,
    pub bids: Vec<Bid>,
    pub bump: u8,
}

impl IntentBids {
    pub const SIZE: usize = 8 + 8 + 4 + (Bid::SIZE * MAX_BIDDERS) + 1;
}

#[account]
pub struct SolvedRecord {
    pub src_chain_id: u64,
    pub src_intent_id: u64,
    pub solver: Pubkey,
    pub token_b_mint: Pubkey,
    pub total_amount: u64,
    pub solved_at: i64,
    pub bump: u8,
}

impl SolvedRecord {
    pub const SIZE: usize = 8 + 8 + 8 + 32 + 32 + 8 + 8 + 1;
}

#[account]
pub struct StealthRegistration {
    pub user: Pubkey,
    pub ephemeral_salt: [u8; 32],
    pub created_at: i64,
    pub bump: u8,
}

impl StealthRegistration {
    pub const SIZE: usize = 8 + 32 + 32 + 8 + 1;
}

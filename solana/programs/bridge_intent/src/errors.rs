use anchor_lang::prelude::*;

#[error_code]
pub enum BridgeIntentError {
    #[msg("Auction duration outside allowed range")]
    InvalidAuctionDuration,
    #[msg("Invalid amount")]
    InvalidAmount,
    #[msg("Invalid reward")]
    InvalidReward,
    #[msg("Invalid destination chain ID")]
    InvalidDestChainId,
    #[msg("Invalid recipient")]
    InvalidRecipient,
    #[msg("Auction ended")]
    AuctionEnded,
    #[msg("Auction still active")]
    AuctionStillActive,
    #[msg("Auction already finalized")]
    AuctionAlreadyFinalized,
    #[msg("Intent already completed")]
    IntentAlreadyCompleted,
    #[msg("Intent not deposited")]
    NotDeposited,
    #[msg("Already deposited")]
    AlreadyDeposited,
    #[msg("Bid not higher than previous")]
    BidNotHigher,
    #[msg("Bidder cap reached")]
    BidderCapReached,
    #[msg("No bids placed")]
    NoBids,
    #[msg("Not the winning solver")]
    NotWinningSolver,
    #[msg("Not authorized")]
    NotAuthorized,
    #[msg("Token mint not in allowlist")]
    TokenMintNotAllowed,
    #[msg("Recipient/amount array length mismatch")]
    ArrayLengthMismatch,
    #[msg("Empty recipients")]
    EmptyRecipients,
    #[msg("Total exceeds expected amount")]
    TotalExceedsExpected,
    #[msg("Total exceeds deposited amount")]
    TotalExceedsDeposited,
    #[msg("Intent already solved on chain2")]
    IntentAlreadySolved,
    #[msg("Too early to cancel")]
    TooEarlyToCancel,
    #[msg("Allowlist index out of bounds")]
    AllowlistIndexOutOfBounds,
    #[msg("Account mint mismatch")]
    AccountMintMismatch,
    #[msg("Account owner mismatch")]
    AccountOwnerMismatch,
    #[msg("Recipient ATAs count mismatch")]
    RecipientCountMismatch,
    #[msg("Stealth registration mismatch")]
    StealthRegistrationMismatch,
    #[msg("Bid must be greater than zero")]
    BidZero,
    #[msg("Arithmetic overflow")]
    Overflow,
}

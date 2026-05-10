pub mod cancel_intent;
pub mod create_intent;
pub mod deposit_and_pickup;
pub mod finalize_auction;
pub mod initialize;
pub mod place_bid;
pub mod register_stealth;
pub mod relayer_admin;
pub mod settle_intent;
pub mod solve_intent_on_chain2;
pub mod sweep_stealth;

// Anchor's #[program] macro requires glob re-exports of every instruction
// module (it scans for the macro-generated `__client_accounts_*` modules).
// The `handler` symbol collisions this produces are warnings only — each call
// site uses `instructions::<module>::handler` explicitly.
#[allow(ambiguous_glob_reexports)]
pub use cancel_intent::*;
#[allow(ambiguous_glob_reexports)]
pub use create_intent::*;
#[allow(ambiguous_glob_reexports)]
pub use deposit_and_pickup::*;
#[allow(ambiguous_glob_reexports)]
pub use finalize_auction::*;
#[allow(ambiguous_glob_reexports)]
pub use initialize::*;
#[allow(ambiguous_glob_reexports)]
pub use place_bid::*;
#[allow(ambiguous_glob_reexports)]
pub use register_stealth::*;
#[allow(ambiguous_glob_reexports)]
pub use relayer_admin::*;
#[allow(ambiguous_glob_reexports)]
pub use settle_intent::*;
#[allow(ambiguous_glob_reexports)]
pub use solve_intent_on_chain2::*;
#[allow(ambiguous_glob_reexports)]
pub use sweep_stealth::*;

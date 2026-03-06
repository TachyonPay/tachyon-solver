# Tachyon Bridge Technical Documentation

## Overview

Tachyon is a cross-chain intent bridge with a competitive solver auction mechanism. Users create intents to bridge tokens across EVM chains, solvers compete to fulfill them, and relayers verify cross-chain completion.

---

## Architecture

```
┌─────────────┐      ┌─────────────┐      ┌─────────────┐
│   User      │──────│  Solver     │──────│  Relayer    │
│  (Chain A)  │      │  Service    │      │  Service    │
└─────────────┘      └─────────────┘      └─────────────┘
       │                    │                    │
       ▼                    ▼                    ▼
┌─────────────────────────────────────────────────────────┐
│              BridgeIntent Smart Contracts               │
│         (Deployed on each supported chain)              │
└─────────────────────────────────────────────────────────┘
```

---

## Encryption Scheme

**ECIES (Elliptic Curve Integrated Encryption Scheme)** for recipient privacy:

- **Purpose**: Encrypt recipient addresses so only the authorized solver can decrypt after winning
- **Flow**:
  1. Frontend encrypts recipient info using relayer's public key
  2. Encrypted data stored by relayer service
  3. Winning solver requests decryption from relayer
  4. Relayer verifies solver won on-chain, returns decrypted recipients
- **Library**: `github.com/ecies/go` (secp256k1 curve)

---

## Database Layer

**PebbleDB** (embedded key-value store):

| Service | Stored Data |
|---------|-------------|
| **Solver** | `activeIntents`, `lastProcessedBlock`, `handlingWins` |
| **Relayer** | `recipientStore`, `intentDetailsStore`, `registeredSolvers` |

- Persistent via Docker volumes
- Separate DB paths per service
- JSON serialization for complex structs

---

## Error Handling

**Smart Contracts**: Custom errors (`InvalidAddress()`, `BidTooLow()`, etc.), `ReentrancyGuard`, `SafeERC20`

**Go Services**: Structured JSON responses, graceful DB fallback, proper HTTP status codes (400/401/403/429/500)

---

## Security Measures

### Smart Contract (BridgeIntentV2)

- OpenZeppelin `AccessControl` (ADMIN, RELAYER, PAUSER, OPERATOR roles)
- `Pausable` for emergencies
- `nonReentrant` on all state-changing functions
- Anti-sniping (2-min extension) + 1% minimum bid increment

### API Security

- API key auth for admin endpoints (`X-API-Key` header)
- Sliding window rate limiting (5-300 req/min per endpoint)
- Constant-time comparison for key validation

---

## Infrastructure & TLS

### Current (AWS EC2)

- TLS termination via Let's Encrypt / Certbot
- HTTPS endpoints for solver and relayer services
- Subdomain routing (`solver.tachyon.pe`, `relayer.tachyon.pe`)

### Upcoming (GCP)

- CI/CD workflow for auto-deploy on push
- Cloud Run / GKE for container orchestration
- Managed SSL certificates
- Blue-green deployments for zero-downtime updates

---

## Test Coverage

**BridgeIntentV2.sol**:

| Metric | Coverage |
|--------|----------|
| Statements | 99.24% |
| Branches | 87.33% |
| Functions | 100% |
| Lines | 100% |

**92 test cases** covering full intent lifecycle, bidding mechanics, access control, and edge cases.

---

## Static Analysis (Slither)

Smart contracts analyzed using **Slither** static analyzer. Summary of findings:

### No Critical/High Severity Issues

| Category | Count | Severity |
|----------|-------|----------|
| Timestamp Dependency | 6 | Low |
| Cyclomatic Complexity | 1 | Informational |
| Naming Convention | 39 | Informational |

### Findings Detail

**1. Timestamp Dependency (Low)** - Uses `block.timestamp` for auction timing comparisons in `placeBid()`, `withdrawBid()`, `finalizeAuction()`, `cancelIntent()`. This is expected behavior for auction mechanics. The ±15s miner manipulation window is acceptable for hour-long auctions.

**2. Cyclomatic Complexity (Informational)** - `solveIntentOnChain2()` has complexity of 13 due to necessary input validation checks and recipient distribution loop.

**3. Naming Convention (Informational)** - Function parameters use `_underscore` prefix. This is intentional to distinguish from state variables.

### Verified Secure

- No reentrancy vulnerabilities
- No unchecked external calls
- No access control issues
- No arithmetic overflow/underflow (Solidity 0.8+)
- No uninitialized storage pointers

---

## Future: TEE Deployment

Relayer in **Trusted Execution Environment**:

- Protects ECIES private key
- Attestation proves code integrity
- Encrypted memory prevents side-channel attacks
- Candidates: AWS Nitro Enclaves, Intel SGX

---

## Future: Safe Multisig as Relayer

Relayer addresses → **Gnosis Safe multisigs**:

- No single point of failure
- Threshold signatures (2-of-3) for settlements
- On-chain governance for key rotation
- TEE nodes sign as Safe owners

---

## Supported Chains (Testnet)

| Chain | Chain ID |
|-------|----------|
| Horizen EON | 845320009 |
| Base Sepolia | 84532 |
| Ethereum Sepolia | 11155111 |
| Rari Testnet | 1918988905 |

---

## Deployment

```bash
# Docker
docker-compose up -d

# Endpoints
Solver:  https://solver.domain.com  (public + /admin/*)
Relayer: https://relayer.domain.com (public + /admin/*)
```

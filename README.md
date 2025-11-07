# ZK-Tachyon: Privacy-Preserving Cross-Chain Bridge Solver

A privacy-first cross-chain bridge solver that uses **stealth addresses + ECIES encryption within TEE** for complete anonymity and provides instant, secure bridging between multiple blockchain networks.

## Problem Statement

Existing cross-chain bridge solutions face critical limitations:

### Traditional Bridges: Finality Delays
- **Problem**: Conventional bridges wait for block finality (15+ minutes on Ethereum, longer on other chains)
- **Impact**: Users experience significant delays, poor UX, and capital inefficiency
- **Our Solution**: Intent-based architecture with competitive solver auctions enables near-instant bridging

### Intent-Based Solutions: Escrow & Secret Complexity
- **Problem**: Solutions like 1inch Fusion use complex escrow mechanisms and secret management
- **Issues**:
  - Difficult implementation and maintenance
  - No privacy preservation for users
  - Centralized secret management creates vulnerabilities
- **Our Solution**: TEE-based anonymous address management with ECIES encryption eliminates complex escrows while ensuring complete privacy

### Oracle-Based Solutions: Settlement Delays
- **Problem**: Across Protocol uses UMA's optimistic oracle system
- **Issues**:
  - Long challenge periods (2+ hours) before settlement
  - Solvers face liquidity uncertainty and delayed capital recovery
  - Inconsistent liquidity provision due to settlement delays
- **Our Solution**: On-chain verification provides immediate settlement, enabling fast and consistent finality

### Privacy Gaps Across All Solutions
- **Problem**: Existing bridges link user identities across chains
- **Impact**: Complete transaction history correlation, privacy violations
- **Our Solution**: Stealth addresses + TEE-based anonymous address storage with ECIES encryption completely breaks user identity links

## Our Comprehensive Solution

| Problem | Traditional Bridges | Intent Solutions | Oracle Solutions | **Our Solution** |
|---------|-------------------|------------------|------------------|------------------|
| **Speed** | 15+ min finality | Fast execution | 2+ hour delays | **Near-instant** |
| **Privacy** | No privacy | No privacy | No privacy | **Complete privacy** |
| **Complexity** | High | Very High | Medium | **Low with TEE** |
| **Liquidity** | Locked capital | Escrow complexity | Delayed recovery | **Fast settlement** |
| **Trust** | Bridge operators | Escrow contracts | Oracle disputes | **Cryptographic proofs** |

## Architecture Overview

```
┌──────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  User (Chain A)  │    │   TEE Relayer    │    │ Solver (Chain B)│
│   Horizen/Base   │    │  ECIES Encrypted │    │  Base/Sepolia   │
└──────────────────┘    └──────────────────┘    └─────────────────┘
         │                       │                       │
         │ 1. Create Intent      │                       │
         │ + Derive Stealth Addr │                       │
         │ + ECIES Encrypt       │                       │
         │   Stealth Addresses   │                       │
         │──────────────────────▶│                       │
         │                       │ 2. Decrypt & Store   │
         │                       │    in TEE Enclave    │
         │                       │                       │
         │                       │ 3. Intent Created    │
         │                       │──────────────────────▶│
         │                       │                       │
         │                       │ 4. Solver Wins       │
         │                       │◀──────────────────────│
         │                       │    Auction & Solves  │
         │                       │                       │
         │                       │◀──────────────────────│ 5. Solve Intent
         │                       │   Send TokenB to      │    on Chain B
         │                       │   Stealth Addresses   │    (unlinkable)
         │                       │                       │
         │                       │ 6. Verify on-chain   │
         │                       │    transaction        │
         │                       │                       │
         │ 7. Settle Intent      │                       │
         │    on Chain A         │                       │
         │◀──────────────────────│                       │
         │                       │                       │
         │ 8. User re-derives    │                       │
         │    stealth private    │                       │
         │    keys to claim      │                       │
         │                       │                       │
         └───────────────────────────────────────────────┘
                Cross-Chain Bridge Complete
            (User identity completely unlinkable)
```

## Privacy Architecture: Stealth Addresses + ECIES + TEE

### Overview

ZK-Tachyon implements a multi-layer privacy architecture combining:
1. **Stealth Addresses**: Client-side cryptographic address derivation for unlinkability
2. **ECIES Encryption**: Secure transmission of stealth addresses to the relayer
3. **TEE Storage**: Isolated storage and decryption within Trusted Execution Environment

The relayer uses **Elliptic Curve Integrated Encryption Scheme (ECIES)** within a **Trusted Execution Environment (TEE)** to receive and store encrypted stealth addresses, ensuring complete privacy for users bridging assets.

### How ECIES Works

ECIES is a hybrid encryption scheme that combines:
- **Elliptic Curve Cryptography (ECC)** for key exchange
- **Symmetric encryption** (AES-256-GCM) for data encryption
- **HMAC** for authentication

### Implementation in Relayer

#### 1. **Key Generation (TEE Initialization)**

When the relayer starts, it generates or loads an ECIES keypair:

The public key is exposed via API endpoint:
```
GET /api/relayer/ecies-public-key
```

#### 2. **Client-Side Encryption (Solver)**

Before storing recipient information, the solver encrypts the data:


#### 3. **TEE Storage and Decryption (Relayer)**

The relayer receives encrypted data and decrypts it within the TEE:

### Security Properties

#### **End-to-End Encryption**
- Recipient addresses are encrypted on the solver side
- Only the relayer (within TEE) can decrypt them
- No intermediary can read the recipient information

#### **TEE Isolation**
- ECIES private key never leaves the TEE enclave
- Decrypted data exists only in TEE-protected memory
- TEE attestation proves code integrity

#### **Forward Secrecy**
- ECIES uses ephemeral keys for each encryption
- Past communications remain secure even if private key is compromised later

#### **Authentication**
- ECIES includes HMAC for authenticated encryption
- Prevents tampering with encrypted data
- Ensures data integrity

### API Endpoints for ECIES

#### Get Relayer's Public Key
```bash
GET http://localhost:3000/api/relayer/ecies-public-key

Response:
{
  "publicKey": "04a1b2c3d4e5f6..."  # Uncompressed ECIES public key (hex)
}
```

#### Store Encrypted Recipients
```bash
POST http://localhost:3000/store-recipients
Content-Type: application/json

{
  "intentId": "123",
  "encryptedData": "04a1b2c3...",  # ECIES encrypted recipient data
  "chainId": 84532
}
```

### Configuration

In [.env](tachyon-prod/.env):

```bash
# ECIES Encryption Configuration
# If not set, a new keypair will be generated on startup
ECIES_PRIVATE_KEY=f35a3856e790c77d364291ff3b7841c675105a9efd6e3ada908b134c9ddd26ff
```

**Security Note**: Store this key securely! In production:
- Use AWS Secrets Manager, HashiCorp Vault, or similar
- Enable TEE attestation to verify enclave integrity
- Rotate keys periodically

## Stealth Addresses for Privacy

### Overview

ZK-Tachyon implements **stealth addresses** on the client side to provide complete recipient privacy. Stealth addresses are cryptographically derived addresses that are unlinkable to the user's original address, ensuring cross-chain anonymity.

### How Stealth Addresses Work

#### 1. **Address Derivation (Client-Side)**

When a user initiates a bridge transaction, the client-side application:
- Derives stealth addresses from the user's signature
- These addresses are deterministic but unlinkable to the source address
- Multiple stealth addresses can be generated for distribution across recipients

#### 2. **Key Properties**

**Deterministic Generation**
- Stealth addresses are derived using cryptographic functions
- Can be regenerated by the user using their signature
- No central registry or storage needed

**Privacy Preservation**
- Stealth addresses appear as random addresses on-chain
- No observable link between source address and stealth addresses
- Observers cannot correlate user activity across chains

**Re-derivability**
- Users can re-derive the private keys for stealth addresses
- Enables claiming funds sent to stealth addresses
- No need to store multiple private keys

#### 3. **Integration with ECIES Encryption**

The complete privacy flow combines stealth addresses with ECIES encryption:

1. **Client-side**: User derives stealth addresses from signature
2. **Client-side**: Stealth addresses are encrypted using relayer's ECIES public key
3. **Transmission**: Only encrypted stealth addresses are sent to relayer
4. **TEE Relayer**: Decrypts and stores stealth addresses in trusted environment
5. **Solver**: Sends tokens to stealth addresses (unlinkable to user)
6. **User**: Re-derives stealth private keys to access funds

### Privacy Guarantees

| Layer | Privacy Mechanism | Protection |
|-------|------------------|------------|
| **Address Generation** | Signature-derived stealth addresses | Source address unlinkability |
| **Transmission** | ECIES encryption | Network-level privacy |
| **Storage** | TEE-isolated decryption | Storage-level privacy |
| **On-Chain** | Stealth address recipients | Blockchain-level privacy |
| **Recovery** | Deterministic re-derivation | No key storage required |

### Security Benefits

**Complete Unlinkability**
- On-chain transactions show only stealth addresses
- Impossible to link bridged funds to original user address
- Cross-chain activity correlation completely prevented

**User-Controlled Privacy**
- Users control stealth address generation
- No trust required in relayer for address privacy
- Deterministic recovery ensures no loss of access

**Multi-Layer Protection**
- Stealth addresses (cryptographic unlinkability)
- ECIES encryption (transmission privacy)
- TEE storage (server-side isolation)
- Intent-based architecture (timing privacy)

### Example Flow

```
User initiates bridge: 100 tokens from Chain A to Chain B

1. Client derives stealth addresses:
   - Stealth Address 1: 0xABC...123 (derived from user signature)
   - Stealth Address 2: 0xDEF...456 (derived from user signature)
   - Stealth Address 3: 0x789...XYZ (derived from user signature)

2. Client encrypts stealth addresses using relayer's ECIES public key

3. Encrypted data sent to relayer and stored in TEE

4. Solver wins auction and distributes tokens:
   - 30 tokens → 0xABC...123
   - 35 tokens → 0xDEF...456
   - 35 tokens → 0x789...XYZ

5. On-chain observers see:
   - Tokens sent to three random addresses
   - No link to original user address

6. User re-derives stealth private keys and claims funds
```

## Solver and Relayer Architecture

### Solver Service (Port 3001)

The solver is responsible for:
- **Event Monitoring**: Continuously polls multiple blockchains for `IntentCreated` events
- **Profitability Analysis**: Calculates if solving an intent is profitable based on:
  - Amount to be bridged
  - Reward offered
  - Current token balances
  - Gas costs
  - Configured profit margin
- **Competitive Bidding**: Participates in on-chain auctions with smart bidding strategies
- **Intent Execution**:
  1. Deposits tokens on source chain to claim intent
  2. Distributes tokens to anonymous addresses on destination chain
  3. Coordinates with relayer for settlement
- **Multi-Chain Support**: Handles intents across multiple chain pairs (Horizen ↔ Base ↔ Sepolia)

#### Key Features
- **Parallel Processing**: Uses Go goroutines for concurrent intent handling
- **Smart Bidding**: Calculates optimal bids considering gas costs and profit margins
- **Balance Management**: Maintains sufficient balances across all chains
- **Error Recovery**: Automatic retry logic for failed transactions

#### Configuration
```bash
# Solver Configuration
MIN_PROFIT_MARGIN=0.02    # 2% minimum profit
MAX_BID_AMOUNT=1000000000000000000000  # 1000 tokens max
BID_INCREMENT=1000000000000000000      # 1 token increment
BALANCE_BUFFER=0.1        # Keep 10% buffer in wallet
```

### Relayer Service (Port 3000)

The relayer provides privacy-preserving settlement services:
- **TEE-Based Storage**: Stores anonymous recipient addresses in trusted execution environment
- **ECIES Encryption**: Accepts only encrypted recipient data (see ECIES section above)
- **Verification**: Verifies intent completion on destination chain
- **Settlement**: Executes final settlement transactions on source chain
- **Solver Registry**: Maintains registry of active solvers and their supported routes

#### Key Features
- **Privacy First**: All sensitive data encrypted and stored in TEE
- **On-Chain Verification**: Direct blockchain state verification for fast settlement
- **Multi-Chain Settlement**: Handles settlements across all configured chains
- **Route Discovery**: Provides API for querying available routes and solvers

#### API Endpoints

**Health Check**
```bash
GET /health
```

**ECIES Public Key** (for encryption)
```bash
GET /api/relayer/ecies-public-key
```

**Store Encrypted Recipients**
```bash
POST /store-recipients
{
  "intentId": "123",
  "encryptedData": "04a1b2...",  # ECIES encrypted
  "chainId": 84532
}
```

**Verify Intent**
```bash
POST /verify
{
  "intentId": "123",
  "network": "base"
}
```

**Settle Intent**
```bash
POST /settle
{
  "intentId": "123",
  "network": "horizen",
  "chain2IntentId": "123",
  "solverAddress": "0x...",
  "destinationChain": "base"
}
```

**Solver Registration**
```bash
POST /api/relayer/register
{
  "address": "0x...",
  "routes": [...],
  "supportedTokens": [...]
}
```

**Route Discovery**
```bash
GET /api/relayer/routes?sourceChain=horizen&tokenA=0x...
```

## Deployment

### Current Deployment

The system is deployed on the following testnet networks with bridge contracts and token support:

#### Network Configuration

**Horizen Testnet**
- Chain ID: `845320009`
- Bridge Contract: `0xc862b8DC344FAf35CE0931c44A222a09cA2Bde33`
- RPC: `https://horizen-rpc-testnet.appchain.base.org`
- Explorer: [Horizen Testnet Explorer](https://horizen-explorer-testnet.appchain.base.org)

**Base Sepolia**
- Chain ID: `84532`
- Bridge Contract: `0x1628Ae09a5f49b8A6B3Ec4Ca20c4C4a40017e13D`
- RPC: `https://base-sepolia.g.alchemy.com/v2/...`
- Explorer: [Base Sepolia Explorer](https://sepolia-explorer.base.org)

**Ethereum Sepolia**
- Chain ID: `11155111`
- Bridge Contract: `0xF3462eB059E0CdA7f20D451Bb2A53Aed937fEfca`
- RPC: `https://eth-sepolia.g.alchemy.com/v2/...`
- Explorer: [Sepolia Etherscan](https://sepolia.etherscan.io)

#### Deployed Token Addresses

**USDC (6 decimals)**
- Horizen: `0xe3fA8D680BDa33dD128a7CCE2e291A39C264975c`
- Base: `0x39b2DfD00D262B7A8d83468C244901c2c1252256`
- Sepolia: `0x49463867BcA4Ba59B6602B7A32bDF5B469B644fa`

**WETH (18 decimals)**
- Horizen: `0x5D5567B92a1d9E9A53Db80DB9D0B570CB0f2C8d4`
- Base: `0x04FAD9adBB9aE63b305aE3c91B027D095487A9e2`
- Sepolia: `0x33E38B8BFfc065110902485932F5417e6cF911A0`

**ZEN (18 decimals)**
- Horizen: `0x10FFc3D770344e1AcCF0bFf351a7D0f3b14153ec`
- Base: `0xE991595dfd6A4b8f9C308cA3a9d469bF76FcA7Da`
- Sepolia: `0xdB822B0dDBEE4b1dDd536bF1D654631D71765c7E`

#### Service Endpoints
- **Solver API**: Port `3001`
- **Relayer API**: Port `3000`

### Quick Start

1. **Clone Repository**
```bash
git clone <repo-url>
cd zk-tachyon-solver/tachyon-prod
```

2. **Configure Environment**
```bash
cp .env.example .env
# Edit .env with your configuration
```

3. **Build Services**
```bash
make build
```

4. **Run Services**
```bash
# Terminal 1 - Relayer
./bin/relayer

# Terminal 2 - Solver
./bin/solver
```


## Configuration

### Chain Configuration

The system uses a flexible chain configuration that supports multiple chains:

```bash
# Number of chains to configure (1-10)
CHAIN_COUNT=3

# Chain 1 Configuration
CHAIN_1_ID=845320009
CHAIN_1_NAME=horizen
CHAIN_1_RPC=https://...
CHAIN_1_BRIDGE=0x...
CHAIN_1_EXPLORER=https://...

# Add more chains as needed (CHAIN_2_*, CHAIN_3_*, etc.)
```

### ECIES Encryption

```bash
# ECIES private key for TEE relayer
# Generate new key if not set (will be printed on first run)
ECIES_PRIVATE_KEY=f35a3856e790c77d364291ff3b7841c675105a9efd6e3ada908b134c9ddd26ff
```

### Solver Parameters

```bash
# Minimum profit margin (0.02 = 2%)
MIN_PROFIT_MARGIN=0.02

# Maximum bid amount in wei
MAX_BID_AMOUNT=1000000000000000000000

# Bid increment in wei
BID_INCREMENT=1000000000000000000

# Balance buffer to keep in wallet (0.1 = 10%)
BALANCE_BUFFER=0.1
```

## Testing

### Unit Tests
```bash
cd tachyon-prod
go test ./...
```

### Integration Tests
```bash
cd tachyon-prod
make test-e2e
```

### Manual Testing

1. **Check Health**
```bash
curl http://localhost:3000/health  # Relayer
curl http://localhost:3001/health  # Solver
```

2. **Get ECIES Public Key**
```bash
curl http://localhost:3000/api/relayer/ecies-public-key
```

3. **Check Active Intents**
```bash
curl http://localhost:3001/active-intents
```

4. **Query Available Routes**
```bash
curl "http://localhost:3000/api/relayer/routes?sourceChain=horizen"
```

## Development

### Project Structure
```
zk-tachyon-solver/
├── tachyon-prod/              # Go implementation
│   ├── cmd/
│   │   ├── solver/           # Solver entry point
│   │   └── relayer/          # Relayer entry point
│   ├── pkg/
│   │   ├── api/              # HTTP API handlers
│   │   ├── blockchain/       # Blockchain clients
│   │   ├── config/           # Configuration
│   │   ├── relayer/          # Relayer logic
│   │   ├── solver/           # Solver logic
│   │   └── types/            # Type definitions
│   ├── contracts/            # ABIs and Solidity
│   ├── test/e2e/             # E2E tests
│   └── scripts/              # Deployment scripts
├── contracts/                # Smart contract source
└── scripts/                  # Deployment scripts
```

### Building from Source
```bash
cd tachyon-prod

# Build both services
make build

# Or build individually
go build -o bin/solver ./cmd/solver
go build -o bin/relayer ./cmd/relayer
```
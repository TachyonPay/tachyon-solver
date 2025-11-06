# Bridge Solver with zkVerify & TEE Privacy Architecture

A privacy-preserving cross-chain bridge solver that leverages **zkVerify** for zero-knowledge proof verification and **Trusted Execution Environments (TEE)** for anonymous address management, ensuring complete privacy for users bridging assets between Horizen and Base networks.

## 🚨 Problem Statement

Existing cross-chain bridge solutions face critical limitations that our architecture addresses:

### ⏳ **Traditional Bridges: Finality Delays**
- **Problem**: Conventional bridges wait for block finality (15+ minutes on Ethereum, longer on other chains)
- **Impact**: Users experience significant delays, poor UX, and capital inefficiency
- **Our Solution**: Intent-based architecture with competitive solver auctions enables near-instant bridging

### 🔒 **Intent-Based Solutions: Escrow & Secret Complexity**
- **Problem**: Solutions like 1inch Fusion use complex escrow mechanisms and secret management
- **Issues**: 
  - Difficult implementation and maintenance
  - No privacy preservation for users
  - Centralized secret management creates vulnerabilities
- **Our Solution**: TEE-based anonymous address management eliminates complex escrows while ensuring complete privacy

### 🐌 **Oracle-Based Solutions: Settlement Delays**  
- **Problem**: Across Protocol uses UMA's optimistic oracle system
- **Issues**:
  - Long challenge periods (2+ hours) before settlement
  - Solvers face liquidity uncertainty and delayed capital recovery
  - Inconsistent liquidity provision due to settlement delays
- **Our Solution**: zkVerify provides immediate cryptographic proof verification, enabling fast and consistent settlement

### 🕵️ **Privacy Gaps Across All Solutions**
- **Problem**: Existing bridges link user identities across chains
- **Impact**: Complete transaction history correlation, privacy violations
- **Our Solution**: TEE-based anonymous address storage completely breaks user identity links

## ✅ **Our Comprehensive Solution**

| Problem | Traditional Bridges | Intent Solutions | Oracle Solutions | **Our Solution** |
|---------|-------------------|------------------|------------------|------------------|
| **Speed** | 15+ min finality | Fast execution | 2+ hour delays | **Near-instant** |
| **Privacy** | ❌ No privacy | ❌ No privacy | ❌ No privacy | **✅ Complete privacy** |
| **Complexity** | High | Very High | Medium | **Low with TEE** |
| **Liquidity** | Locked capital | Escrow complexity | Delayed recovery | **Fast settlement** |
| **Trust** | Bridge operators | Escrow contracts | Oracle disputes | **Cryptographic proofs** |

## 🏗️ Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   User (Chain1) │    │   TEE Relayer    │    │ Solver (Chain2) │
│    Horizen      │    │   Private Vault  │    │     Base        │
└─────────────────┘    └──────────────────┘    └─────────────────┘
         │                       │                       │
         │ 1. Create Intent      │                       │
         │ + Anonymous Addresses │                       │
         │──────────────────────▶│                       │
         │                       │ 2. Store Anon Data   │
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
         │                       │   Send TokenB to      │    on Chain2
         │                       │   Anonymous Addresses │    (Base)
         │                       │                       │
         │                       │ 6. Monitor Chain2     │
         │                       │    Light Client (LC)  │
         │                       │    Generate zkProof   │
         │                       │    of Chain2 Tx       │
         │                       │                       │
         │ 7. Verify Proof via   │                       │
         │    zkVerify & Settle  │                       │
         │    Funds on Chain1    │                       │
         │◀──────────────────────│                       │
         │                       │                       │
         │                                               │
         └───────────────────────────────────────────────┘
                    Cross-Chain Bridge Complete
                   (User identity unlinkable)
```

## 🔐 Privacy-First Design

### TEE-Based Anonymous Address Management

The relayer operates within a **Trusted Execution Environment (TEE)** to ensure complete privacy:

- **Secure Storage**: Anonymous recipient addresses are stored exclusively within the TEE enclave
- **Zero Knowledge**: The relayer cannot access or leak recipient information outside the secure environment
- **Link Breaking**: By using anonymous addresses generated by users, we completely break the link between the user's identity on Horizen and their activity on Base
- **Attestation**: TEE provides cryptographic proof that the code is running in a secure, unmodified environment

### zkVerify Integration

**zkVerify** is used to verify zero-knowledge proofs generated by the light client:

1. **Light Client Proof Generation**: When the solver completes an intent on Chain2, the relayer's light client generates a zero-knowledge proof of the transaction
2. **zkVerify Verification**: The proof is submitted to zkVerify for verification without revealing transaction details
3. **Settlement with Proof**: The relayer uses the `/settle-with-proof` endpoint to ensure cryptographic verification before settling funds on Chain1 (Horizen)

## 🚀 Key Features

### 🛡️ Privacy Preservation
- **Anonymous Recipients**: Users generate anonymous addresses that are stored securely in TEE
- **Link Breaking**: Complete separation between user identity on Horizen and Base activity
- **Zero-Knowledge Proofs**: Transaction validity proven without revealing sensitive information

### ⚡ Cross-Chain Bridge Solving
- **Intent-Based Architecture**: Users express intent to bridge assets across chains
- **Competitive Auction**: Solvers compete to fulfill intents at the best rates
- **Automated Execution**: End-to-end automation from intent creation to settlement

### 🔍 zkVerify Proof System
- **Light Client Integration**: Generates proofs of cross-chain transaction completion
- **zkVerify Verification**: Leverages zkVerify's robust proof verification infrastructure
- **Trust Minimization**: Reduces trust assumptions through cryptographic proofs
- **Settle-with-Proof**: Dedicated endpoint for cryptographically verified settlements

## 🏃‍♂️ How It Works

### 1. Intent Creation with Anonymous Addresses
Users create intents specifying:
- Token A (on origin chain) and Token B (on destination chain)
- Amount to bridge
- Anonymous recipient addresses (generated client-side)
- Distribution amounts across recipients

### 2. TEE Secure Storage
The relayer's TEE enclave securely stores the mapping:
```
Intent ID → Anonymous Recipients (encrypted in TEE)
```

### 3. Solver Competition & Execution
- Solvers compete in auctions to fulfill intents
- Winning solver deposits tokens and solves the intent on destination chain
- Funds are distributed to anonymous addresses, breaking user identity links

### 4. zkVerify Proof & Settlement
1. Light client generates proof of transaction completion on destination chain
2. Relayer submits proof to zkVerify for verification
3. Once proof is finalized, settlement is authorized on origin chain
4. Solver receives escrowed funds after cryptographic verification

## 🛠️ Technical Implementation

### Solver and Relayer (Rust/Go Implementation)

The production solver and relayer are implemented in Rust/Go in the `tachyon-prod/` directory for optimal performance and security.

#### Key Components:
- **Multi-Chain Support**: Operates across Horizen and Base networks
- **Auction Participation**: Competitive bidding for profitable intents
- **Cross-Chain Execution**: Handles token approvals, deposits, and distributions
- **TEE Integration**: Secure enclave for anonymous address management
- **Light Client**: Generates zero-knowledge proofs of cross-chain transactions
- **zkVerify Interface**: Submits and verifies proofs through zkVerify
- **Settlement with Proofs**: Cryptographic verification before settlement

#### Proof Verification Flow
1. **Proof Submission**: SP1 proof submitted to zkVerify API
2. **Optimistic Verification**: Immediate validation of proof structure
3. **Finalization Polling**: Relayer polls zkVerify until proof is finalized on-chain
4. **Settlement Authorization**: Only finalized proofs authorize settlement transactions

See `tachyon-prod/README.md` for detailed setup and configuration instructions.

## 🔧 Setup & Configuration

### Prerequisites
- Rust (for solver/relayer)
- Go (for relayer components)
- Node.js 18+ (for contract deployment)
- Private keys for solver wallets on both chains
- Access to zkVerify API
- TEE-enabled hardware for relayer (optional)

### Environment Variables
```bash
# Network Configuration
HORIZEN_TESTNET_RPC_URL=https://...
BASE_SEPOLIA_RPC_URL=https://...

# Contract Addresses
BRIDGE_CONTRACT_HORIZEN_LATEST=0x...
BRIDGE_CONTRACT_BASE_LATEST=0x...

# Solver Configuration
PRIVATE_KEY=0x...
MIN_PROFIT_MARGIN=0.02
MAX_BID_AMOUNT=1000
BID_INCREMENT=1

# TEE Relayer
RELAYER_URL=https://tee-relayer.example.com
ZKVERIFY_API_URL=https://zkverify.api.url
```

### Installation & Running

#### Deploy Smart Contracts
```bash
# Install hardhat dependencies
npm install

# Deploy contracts
npx hardhat run scripts/deploy.ts --network horizen_testnet
npx hardhat run scripts/deploy.ts --network base_sepolia

# Mint test tokens (optional)
npx hardhat run scripts/mintTokens.ts --network horizen_testnet
```

#### Run Solver & Relayer
See [tachyon-prod/README.md](tachyon-prod/README.md) for detailed instructions on building and running the Rust/Go implementation.

## 🔒 Security Considerations

### TEE Security
- **Attestation**: Verify TEE attestation before trusting relayer
- **Code Integrity**: Ensure relayer code hasn't been tampered with
- **Secure Communication**: All communication with TEE over encrypted channels

### zkVerify Trust
- **Proof Verification**: All cross-chain settlements require valid zkVerify proofs
- **Light Client Security**: Light client implementation audited for correctness
- **Verification Parameters**: Proper setup of zkVerify verification parameters

### Privacy Guarantees
- **Anonymous Addresses**: Generated client-side, never linked to user identity
- **TEE Isolation**: Recipient data never leaves the secure enclave
- **Zero-Knowledge**: Proofs reveal validity without exposing transaction details

## 🌐 Network Support

### Horizen Testnet
- **Chain ID**: 845320009
- **Explorer**: https://horizen-explorer-testnet.appchain.base.org
- **Privacy Features**: Enhanced with TEE-based anonymous address management

### Base Sepolia
- **Chain ID**: 84532  
- **Explorer**: https://sepolia-explorer.base.org
- **Integration**: Seamless cross-chain settlement with zkVerify proofs

## 🤝 Contributing

We welcome contributions to enhance the privacy and security features:

1. **TEE Improvements**: Enhance enclave security and performance
2. **zkVerify Integration**: Optimize proof generation and verification
3. **Privacy Features**: Additional anonymity and unlinkability improvements
4. **Testing**: Comprehensive testing of privacy guarantees

## 📜 License

MIT License - see [LICENSE](LICENSE) for details

## 🔗 Links

- **zkVerify**: [zkverify.io](https://zkverify.io)
- **Horizen**: [horizen.io](https://horizen.io)  
- **Base**: [base.org](https://base.org)
- **TEE Standards**: [Trusted Execution Environment specifications](https://trustedcomputinggroup.org/)

---

*This bridge solver demonstrates how zkVerify and TEE technologies can be combined to create truly private cross-chain transactions, breaking user links while maintaining cryptographic security guarantees.*

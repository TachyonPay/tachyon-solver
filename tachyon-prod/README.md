# Tachyon-Prod: Go Implementation of ZK-Tachyon Solver

This is a production-ready Golang implementation of the ZK-Tachyon cross-chain bridge solver and relayer system. This version removes zkVerify proof verification and implements basic verification for development and testing purposes.

## Architecture Overview

The system consists of two main services:

### 1. Solver Service (Port 3001)
- **Event Polling**: Monitors blockchain for new bridge intents
- **Bidding Logic**: Evaluates profitability and participates in auctions
- **Intent Solving**: Deposits tokens and solves intents on destination chains
- **Cross-Chain Execution**: Coordinates cross-chain token distribution

### 2. Relayer Service (Port 3000)
- **Privacy Management**: Stores anonymous recipient information (TEE simulation)
- **Basic Verification**: Verifies intent completion on destination chain
- **Settlement**: Finalizes intent settlement with on-chain verification

## Features

✅ **Automated Intent Solving**: Continuously monitors and solves profitable bridge intents
✅ **Competitive Bidding**: Smart bidding algorithm with profit margin calculations
✅ **Multi-Network Support**: Horizen Testnet and Base Sepolia
✅ **Privacy Preservation**: Anonymous recipient storage (simulates TEE)
✅ **Basic Verification**: On-chain verification without zkVerify integration
✅ **RESTful APIs**: Full HTTP API for monitoring and control
✅ **Graceful Shutdown**: Clean shutdown handling for both services

## Project Structure

```
tachyon-prod/
├── cmd/
│   ├── solver/           # Solver service entry point
│   │   └── main.go
│   └── relayer/          # Relayer service entry point
│       └── main.go
├── pkg/
│   ├── api/              # HTTP API handlers
│   │   ├── solver_handlers.go
│   │   └── relayer_handlers.go
│   ├── blockchain/       # Blockchain client and contract interaction
│   │   └── client.go
│   ├── config/           # Configuration management
│   │   └── config.go
│   ├── relayer/          # Relayer service logic
│   │   └── relayer.go
│   ├── solver/           # Solver service logic
│   │   └── solver.go
│   └── types/            # Shared type definitions
│       └── types.go
├── contracts/            # Contract ABIs and Solidity files
│   ├── BridgeIntent.json
│   └── BridgeIntent.sol
├── .env.example          # Example environment configuration
├── go.mod                # Go module definition
└── README.md             # This file
```

## Prerequisites

- **Go 1.21+**: [Download](https://golang.org/dl/)
- **Ethereum Wallet**: Private key with testnet funds
- **RPC Access**: Access to Horizen Testnet and Base Sepolia RPCs

## Installation

### 1. Clone and Navigate to Directory

```bash
cd tachyon-prod
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Configure Environment

Copy the example environment file and update with your values:

```bash
cp .env.example .env
```

Edit `.env` and configure:

```bash
# Your wallet private key (KEEP SECURE!)
PRIVATE_KEY=0x...

# Contract addresses (update after deployment)
BRIDGE_CONTRACT_HORIZEN_LATEST=0x...
BRIDGE_CONTRACT_BASE_LATEST=0x...

# Solver configuration (optional, defaults provided)
MIN_PROFIT_MARGIN=0.02        # 2% profit margin
MAX_BID_AMOUNT=1000000000000000000000  # 1000 tokens in wei
BID_INCREMENT=1000000000000000000      # 1 token in wei
BALANCE_BUFFER=0.1            # Keep 10% buffer

# Service ports
SOLVER_PORT=3001
RELAYER_PORT=3000
RELAYER_URL=http://localhost:3000
```

## Building

### Build Both Services

```bash
# Build solver
go build -o bin/solver ./cmd/solver

# Build relayer
go build -o bin/relayer ./cmd/relayer
```

### Build with Optimizations

```bash
go build -ldflags="-s -w" -o bin/solver ./cmd/solver
go build -ldflags="-s -w" -o bin/relayer ./cmd/relayer
```

## Running

### Start Relayer Service (Terminal 1)

```bash
./bin/relayer
```

Or run directly:

```bash
go run ./cmd/relayer
```

### Start Solver Service (Terminal 2)

```bash
./bin/solver
```

Or run directly:

```bash
go run ./cmd/solver
```

## API Documentation

### Solver API (Port 3001)

#### Health Check
```bash
GET /health
```

Response:
```json
{
  "status": "healthy",
  "service": "bridge-solver",
  "timestamp": "2025-11-05T10:30:00Z",
  "activeIntents": 5
}
```

#### Get Status
```bash
GET /status
```

Response:
```json
{
  "isRunning": true,
  "activeIntents": {...},
  "walletAddress": "0x...",
  "balances": {
    "horizen": "1000000000000000000",
    "base": "2000000000000000000"
  }
}
```

#### Get Active Intents
```bash
GET /active-intents
```

#### Get Intent Status
```bash
GET /intent-status/:intentId
GET /intent-status/:intentId/:network
```

#### Start Solver
```bash
POST /start
```

#### Stop Solver
```bash
POST /stop
```

### Relayer API (Port 3000)

#### Health Check
```bash
GET /health
```

#### Store Recipients
```bash
POST /store-recipients
Content-Type: application/json

{
  "intentId": "123",
  "recipients": ["0x...", "0x..."],
  "amounts": ["1000000000000000000", "2000000000000000000"],
  "chainId": 84532
}
```

#### Get Recipients
```bash
GET /get-recipients/:intentId
```

#### List Stored Intents
```bash
GET /list-stored-intents
```

#### Delete Recipients
```bash
DELETE /delete-recipients/:intentId
```

#### Verify Intent
```bash
POST /verify
Content-Type: application/json

{
  "intentId": "123",
  "network": "base"
}
```

#### Settle Intent
```bash
POST /settle
Content-Type: application/json

{
  "intentId": "123",
  "network": "horizen",
  "chain2IntentId": "123",
  "solverAddress": "0x...",
  "destinationChain": "base"
}
```

## Configuration Options

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `HORIZEN_TESTNET_RPC_URL` | Horizen Testnet RPC endpoint | - |
| `BASE_SEPOLIA_RPC_URL` | Base Sepolia RPC endpoint | - |
| `BRIDGE_CONTRACT_HORIZEN_LATEST` | Horizen bridge contract address | - |
| `BRIDGE_CONTRACT_BASE_LATEST` | Base bridge contract address | - |
| `PRIVATE_KEY` | Wallet private key (with 0x prefix) | - |
| `MIN_PROFIT_MARGIN` | Minimum profit margin (0.02 = 2%) | 0.02 |
| `MAX_BID_AMOUNT` | Maximum bid amount in wei | 1000 ether |
| `BID_INCREMENT` | Bid increment in wei | 1 ether |
| `BALANCE_BUFFER` | Balance buffer (0.1 = 10%) | 0.1 |
| `RELAYER_URL` | Relayer service URL | http://localhost:3000 |
| `SOLVER_PORT` | Solver service port | 3001 |
| `RELAYER_PORT` | Relayer service port | 3000 |

## How It Works

### Intent Lifecycle

1. **Intent Creation**: User creates a bridge intent on the source chain
2. **Event Detection**: Solver detects `IntentCreated` event
3. **Profitability Check**: Solver calculates if intent is profitable
4. **Bidding**: Solver places competitive bids during auction period
5. **Auction End**: When auction ends, winning solver is determined
6. **Deposit**: Winner deposits tokens to pick up user's tokens
7. **Cross-Chain Solve**: Solver distributes tokens on destination chain
8. **Settlement**: Relayer verifies and settles the intent
9. **Completion**: User receives funds, solver receives profit

### Profit Calculation

```go
// Minimum profitable bid
minBid = amountA + reward

// Apply profit margin
profitMargin = minBid * MIN_PROFIT_MARGIN
targetBid = minBid - profitMargin

// Check affordability
maxAffordable = balance * (1 - BALANCE_BUFFER)
canAfford = targetBid <= maxAffordable && targetBid <= MAX_BID_AMOUNT
```

### Basic Verification Flow

```
1. Solver solves intent on destination chain
2. Relayer receives settlement request
3. Relayer verifies intent is marked as "solved" on destination chain
4. If verified, relayer calls settleIntentWithChain2Verification()
5. Settlement transaction is executed on source chain
```

## Development

### Running Tests

```bash
go test ./...
```

### Running with Debug Logging

Edit the main files to set log level:

```go
log.SetLevel(log.DebugLevel)
```

### Code Formatting

```bash
go fmt ./...
```

### Linting

```bash
golangci-lint run
```

## Differences from TypeScript Version

### Removed Features
- ❌ zkVerify proof submission and verification
- ❌ SP1 proof handling
- ❌ Complex proof verification middleware

### Changes
- ✅ Basic on-chain verification instead of zkVerify
- ✅ Simplified settlement flow
- ✅ Direct blockchain state checks
- ✅ Go-native concurrency with goroutines
- ✅ Structured logging with logrus
- ✅ Type-safe contract interactions

## Security Considerations

⚠️ **IMPORTANT SECURITY NOTES**:

1. **Private Key Management**:
   - Never commit `.env` file to version control
   - Use environment variables or key management systems in production
   - Consider using hardware wallets or HSMs

2. **TEE Simulation**:
   - Current implementation stores recipients in memory
   - Production should use proper Trusted Execution Environment
   - Consider encrypted storage or secure enclaves

3. **Basic Verification**:
   - This version uses basic on-chain verification
   - Not suitable for production without zkVerify or similar proof system
   - Vulnerable to chain reorganizations

4. **Gas Management**:
   - Monitor gas prices and adjust limits
   - Implement gas price strategies for profitability
   - Handle transaction failures gracefully

## Troubleshooting

### Solver Not Detecting Intents

- Check RPC URLs are accessible
- Verify contract addresses are correct
- Ensure wallet has sufficient funds
- Check event polling logs

### Bids Failing

- Verify sufficient token balance
- Check token approvals for bridge contract
- Ensure auction hasn't ended
- Review gas price settings

### Settlement Failures

- Verify intent is solved on destination chain
- Check relayer authorization in contract
- Ensure proper intent IDs are provided
- Review transaction logs

## Contributing

This is a production implementation of the ZK-Tachyon solver. For issues or improvements:

1. Check existing issues
2. Create detailed bug reports
3. Submit pull requests with tests
4. Follow Go best practices

## License

See main project license.

## Support

For questions or issues:
- Check logs with debug level enabled
- Review API responses for error details
- Verify configuration and environment
- Check blockchain explorer for transaction details

## Roadmap

Future enhancements planned:

- [ ] zkVerify integration (optional)
- [ ] Database persistence for recipient store
- [ ] Advanced gas optimization strategies
- [ ] Multi-token support
- [ ] Monitoring and alerting
- [ ] Performance metrics and dashboards
- [ ] Docker containerization
- [ ] Kubernetes deployment manifests

# Tachyon-Prod Setup Guide

Production-ready Golang implementation of ZK-Tachyon cross-chain bridge solver and relayer.

## Quick Start

### Prerequisites

- Go 1.21+
- Ethereum wallet with testnet funds
- RPC access to Horizen Testnet and Base Sepolia

### Installation

```bash
cd tachyon-prod
go mod download
cp .env.example .env
```

### Configuration

Edit `.env`:

```bash
# Chain Pair Configuration (supports up to 10 pairs)
PAIR_COUNT=1

# Pair 1: Source and Destination Chain
PAIR_1_SOURCE_CHAIN=horizen
PAIR_1_SOURCE_CHAIN_ID=845320009
PAIR_1_SOURCE_RPC=https://horizen-rpc-testnet.appchain.base.org
PAIR_1_SOURCE_BRIDGE=0x8d692017aEA872De988AC27FfD6B9Fed3FF0FC13
PAIR_1_SOURCE_EXPLORER=https://horizen-explorer-testnet.appchain.base.org

PAIR_1_DEST_CHAIN=base
PAIR_1_DEST_CHAIN_ID=84532
PAIR_1_DEST_RPC=https://sepolia.base.org
PAIR_1_DEST_BRIDGE=0xd2C8C5C6DAD1be31077b0EeDEb78fcB62f7e1066
PAIR_1_DEST_EXPLORER=https://sepolia-explorer.base.org

# Wallet
PRIVATE_KEY=0x...

# Solver Configuration
MIN_PROFIT_MARGIN=0.02
MAX_BID_AMOUNT=1000000000000000000000
BID_INCREMENT=1000000000000000000
BALANCE_BUFFER=0.1

# Service Configuration
SOLVER_PORT=3001
RELAYER_PORT=3000
RELAYER_URL=http://localhost:3000
```

The solver now supports configuring up to 10 chain pairs. Each pair defines a bidirectional bridge:
- Intents created on the source chain will be solved on the destination chain
- The solver monitors both chains and can handle intents in either direction
- Add more pairs by incrementing PAIR_COUNT and adding PAIR_2_*, PAIR_3_*, etc.

### Build

```bash
make build
```

Or manually:

```bash
go build -o bin/solver ./cmd/solver
go build -o bin/relayer ./cmd/relayer
```

### Run

Terminal 1 - Relayer:
```bash
make run-relayer
```

Terminal 2 - Solver:
```bash
make run-solver
```

## Architecture

### Solver Service (Port 3001)
- Monitors blockchain for bridge intents
- Evaluates profitability and places bids
- Executes cross-chain token distribution
- Coordinates with relayer for settlement

### Relayer Service (Port 3000)
- Stores recipient information securely
- Verifies intent completion on-chain
- Executes final settlement transactions

## API Endpoints

### Solver API

```bash
GET  /health                    # Health check
GET  /status                    # Service status
GET  /active-intents            # List active intents
GET  /intent-status/:intentId   # Intent details
POST /start                     # Start solver
POST /stop                      # Stop solver
```

### Relayer API

```bash
GET    /health                         # Health check
POST   /store-recipients               # Store recipient data
GET    /get-recipients/:intentId       # Retrieve recipients
GET    /list-stored-intents            # List all intents
DELETE /delete-recipients/:intentId    # Delete recipients
POST   /verify                         # Verify intent
POST   /settle                         # Settle intent
```

## Intent Flow

1. User creates bridge intent on source chain
2. Solver detects IntentCreated event
3. Solver evaluates profitability
4. Solver places competitive bid
5. Auction ends, winner determined
6. Winner deposits tokens on source chain
7. Winner distributes tokens on destination chain
8. Relayer verifies and settles intent
9. Settlement completed on source chain

## Development

### Testing

```bash
go test ./...
```

End-to-end tests:
```bash
make test-e2e
```

### Code Quality

```bash
go fmt ./...
golangci-lint run
```

### Debug Mode

Set in main files:
```go
log.SetLevel(log.DebugLevel)
```

## Project Structure

```
tachyon-prod/
├── cmd/
│   ├── solver/          # Solver entry point
│   └── relayer/         # Relayer entry point
├── pkg/
│   ├── api/             # HTTP handlers
│   ├── blockchain/      # Contract interaction
│   ├── config/          # Configuration
│   ├── relayer/         # Relayer logic
│   ├── solver/          # Solver logic
│   └── types/           # Type definitions
├── contracts/           # ABIs and Solidity
├── test/e2e/            # End-to-end tests
├── .env.example         # Example config
├── Makefile             # Build commands
└── SETUP.md             # This file
```

## Security Notes

1. Never commit `.env` to version control
2. Use proper key management in production
3. Current recipient storage is in-memory (TEE simulation)
4. Basic verification used (not production-ready without zkVerify)
5. Monitor gas prices and transaction failures

## Troubleshooting

**Solver not detecting intents:**
- Verify RPC URLs are accessible
- Check contract addresses
- Ensure wallet has funds

**Bids failing:**
- Check token balance and approvals
- Verify auction hasn't ended
- Review gas settings

**Settlement failures:**
- Confirm intent solved on destination
- Verify relayer authorization
- Check transaction logs

## Make Commands

```bash
make build              # Build both services
make build-solver       # Build solver only
make build-relayer      # Build relayer only
make run-solver         # Run solver
make run-relayer        # Run relayer
make test               # Run tests
make test-e2e           # Run E2E tests
make clean              # Clean build artifacts
make fmt                # Format code
make lint               # Run linter
```

## Configuration Reference

### Chain Pair Configuration

| Variable | Description | Required |
|----------|-------------|----------|
| PAIR_COUNT | Number of chain pairs (1-10) | Yes |
| PAIR_N_SOURCE_CHAIN | Source chain name | Yes |
| PAIR_N_SOURCE_CHAIN_ID | Source chain ID | Yes |
| PAIR_N_SOURCE_RPC | Source chain RPC URL | Yes |
| PAIR_N_SOURCE_BRIDGE | Source bridge contract | Yes |
| PAIR_N_SOURCE_EXPLORER | Source chain explorer | No |
| PAIR_N_DEST_CHAIN | Destination chain name | Yes |
| PAIR_N_DEST_CHAIN_ID | Destination chain ID | Yes |
| PAIR_N_DEST_RPC | Destination chain RPC URL | Yes |
| PAIR_N_DEST_BRIDGE | Destination bridge contract | Yes |
| PAIR_N_DEST_EXPLORER | Destination chain explorer | No |

Replace N with the pair number (1-10).

### Other Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| PRIVATE_KEY | Wallet private key | Required |
| MIN_PROFIT_MARGIN | Minimum profit (0.02 = 2%) | 0.02 |
| MAX_BID_AMOUNT | Max bid in wei | 1000 ether |
| BID_INCREMENT | Bid increment in wei | 1 ether |
| BALANCE_BUFFER | Balance buffer (0.1 = 10%) | 0.1 |
| RELAYER_URL | Relayer URL | http://localhost:3000 |
| SOLVER_PORT | Solver port | 3001 |
| RELAYER_PORT | Relayer port | 3000 |

## Support

For issues:
- Enable debug logging
- Check API responses
- Review blockchain explorer
- Verify configuration

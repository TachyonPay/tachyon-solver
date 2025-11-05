# End-to-End Bridge Flow Tests

This directory contains comprehensive end-to-end tests for the Tachyon-Prod Go implementation of the cross-chain bridge solver.

## Overview

The E2E tests simulate the complete bridge flow:
1. Deploy test tokens on both chains
2. Create a bridge intent on the source chain (Horizen)
3. Store anonymous recipient information in the relayer
4. Place a bid and participate in the auction
5. Finalize the auction and deposit tokens
6. Solve the intent on the destination chain (Base)
7. Verify the intent was solved
8. Settle the intent through the relayer

## Prerequisites

### Services Running

Ensure both services are running locally:

```bash
# Terminal 1 - Start Relayer
cd ../../
./bin/relayer

# Terminal 2 - Start Solver
cd ../../
./bin/solver
```

### Environment Configuration

1. Copy the test environment template:
   ```bash
   cp .env.test.example .env.test
   ```

2. Update `.env.test` with your values:
   ```bash
   # Network RPCs
   HORIZEN_TESTNET_RPC_URL=https://horizen-rpc-testnet.appchain.base.org
   BASE_SEPOLIA_RPC_URL=https://sepolia.base.org

   # Contract addresses (deployed bridge contracts)
   BRIDGE_CONTRACT_HORIZEN_LATEST=0x...
   BRIDGE_CONTRACT_BASE_LATEST=0x...

   # Test wallet with funds on both networks
   PRIVATE_KEY=0x...

   # Service URLs (if not default)
   SOLVER_URL=http://localhost:3001
   RELAYER_URL=http://localhost:3000
   ```

### Testnet Funds

Ensure your test wallet has:
- **Horizen Testnet**: ETH for gas + test tokens
- **Base Sepolia**: ETH for gas + test tokens

## Project Structure

```
test/e2e/
├── .env.test.example      # Environment template
├── .env.test             # Your config (gitignored)
├── complete_flow_test.go # Main E2E test
├── utils/
│   └── helpers.go        # Test utilities
├── contracts/
│   └── mock_erc20.go     # ERC20 contract helpers
├── README.md             # This file
└── run_test.sh          # Test runner script
```

## Running the Tests

### Quick Start

```bash
# Run the complete flow test
./run_test.sh
```

### Manual Execution

```bash
# Build the test
go build -o e2e_test complete_flow_test.go

# Run the test
./e2e_test
```

### With Go Run

```bash
go run complete_flow_test.go
```

## Test Flow Details

### Step 1: Deploy Test Tokens

The test deploys two ERC20 tokens:
- **TokenA** on Horizen Testnet (source)
- **TokenB** on Base Sepolia (destination)

```go
tokenA := deployMockERC20("Token A", "TKA", parseEther("1000000"))
tokenB := deployMockERC20("Token B", "TKB", parseEther("1000000"))
```

### Step 2: Generate Recipients

Creates anonymous recipients for privacy testing:

```go
recipients := []string{
  randomAddress(), // Recipient 1
  randomAddress(), // Recipient 2
  randomAddress(), // Recipient 3
}

amounts := []string{
  "30000000000000000000", // 30 ETH
  "35000000000000000000", // 35 ETH
  "30000000000000000000", // 30 ETH
}
```

### Step 3: Store Recipients in Relayer

Stores anonymous recipient info before intent creation:

```go
POST /store-recipients
{
  "intentId": "123",
  "recipients": ["0x...", "0x...", "0x..."],
  "amounts": ["30...", "35...", "30..."],
  "chainId": 84532
}
```

### Step 4: Create Intent

Creates a bridge intent on Horizen:

```go
createIntent(
  tokenA,        // Source token
  tokenB,        // Destination token
  parseEther("95"), // Amount
  parseEther("5"),  // Reward
  20            // Auction duration (seconds)
)
```

### Step 5: Bidding & Auction

1. Place a bid:
   ```go
   placeBid(intentId, parseEther("98"))
   ```

2. Wait for auction to end (20 seconds)

3. Finalize auction:
   ```go
   finalizeAuction(intentId)
   ```

### Step 6: Deposit Tokens

Winner deposits tokens to pick up user's funds:

```go
depositAndPickup(intentId)
```

### Step 7: Solve on Destination Chain

Distribute tokens to recipients on Base:

```go
solveIntentOnChain2(
  intentId,
  user,
  tokenB,
  expectedAmountB,
  recipients,
  amounts
)
```

### Step 8: Verification

Check if intent was solved:

```go
solved := verifyIntentSolved(intentId)
// Should return: true
```

### Step 9: Settlement

Settle the intent through relayer:

```go
POST /settle
{
  "intentId": "123",
  "network": "horizen",
  "chain2IntentId": "123",
  "solverAddress": "0x...",
  "destinationChain": "base"
}
```

## Expected Output

```
🚀 Starting complete cross-chain bridge flow test (Go implementation)...

📍 Chain IDs: Horizen=845320009, Base=84532
👛 Wallet Address: 0x...

🏥 Checking service health...
✅ Solver service is healthy
✅ Relayer service is healthy

🎯 Target Intent ID: 124 (will be created next)

🪙 STEP 1: Deploying test tokens...
✅ TokenA (Horizen): 0x...
✅ TokenB (Base): 0x...

📋 STEP 1.5: Generating recipients and storing in relayer...
🎯 Recipients: [0x..., 0x..., 0x...]
💰 Amounts: [30, 35, 30] ETH
✅ Recipients stored in relayer

🎯 STEP 2: Creating intent on Horizen Testnet...
✅ Approved tokens for intent creation
✅ Intent created on Horizen
🎯 Actual Intent ID: 124
📍 Transaction: https://horizen-explorer-testnet.appchain.base.org/tx/0x...

🏆 STEP 3: Placing bid before auction ends...
✅ Bid placed successfully

⏳ STEP 4: Waiting 25 seconds for auction to end...
Waiting for auction..........Done!

🔨 STEP 5: Finalizing auction...
✅ Auction finalized

💰 STEP 6: Depositing and picking up...
✅ Approved tokens for deposit
✅ Deposit completed

🌐 STEP 7: Solving intent on Base Sepolia...
🔍 Intent already solved on Base: false
✅ Approved TokenB for bridge contract
✅ Intent solved on Base
📍 Transaction: https://sepolia-explorer.base.org/tx/0x...

⏳ Waiting for state to be updated...

🔍 STEP 8: Verifying intent solved on destination...
✅ Intent 124 solved status: true

💰 STEP 9: Calling relayer to settle...
✅ Settlement transaction: 0x...
✅ Intent settled successfully!

🎉 Complete flow test finished successfully!
🎯 Final Intent ID used: 124
```

## Troubleshooting

### Service Health Check Fails

**Problem**: Solver or Relayer health check fails

**Solution**:
1. Ensure services are running:
   ```bash
   curl http://localhost:3000/health
   curl http://localhost:3001/health
   ```

2. Check service logs for errors

3. Verify ports are not in use by other processes

### Insufficient Funds

**Problem**: Transaction fails due to insufficient balance

**Solution**:
1. Check wallet balances:
   ```bash
   # Use block explorer or:
   curl http://localhost:3001/status
   ```

2. Get testnet tokens:
   - Horizen Testnet: [Faucet URL]
   - Base Sepolia: https://faucet.base.org

### Contract Address Invalid

**Problem**: "Failed to connect to contract"

**Solution**:
1. Verify contract addresses in `.env.test`
2. Ensure contracts are deployed on correct networks
3. Check explorer links to confirm contracts exist

### Auction Duration Too Short

**Problem**: Bid placed after auction ends

**Solution**:
Increase `AUCTION_DURATION` in `.env.test`:
```bash
AUCTION_DURATION=60  # 60 seconds instead of 20
```

### Recipients Not Found

**Problem**: "Recipients not found for intent"

**Solution**:
1. Ensure relayer is running before test
2. Check relayer logs for storage errors
3. Verify `/store-recipients` endpoint works:
   ```bash
   curl -X POST http://localhost:3000/list-stored-intents
   ```

### Transaction Wait Timeout

**Problem**: "Transaction wait timeout"

**Solution**:
1. Check RPC connectivity
2. Verify network is not congested
3. Increase gas price in test
4. Use alternative RPC endpoint

## Test Configuration Options

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `HORIZEN_TESTNET_RPC_URL` | Horizen RPC endpoint | Required |
| `BASE_SEPOLIA_RPC_URL` | Base RPC endpoint | Required |
| `BRIDGE_CONTRACT_HORIZEN_LATEST` | Horizen bridge address | Required |
| `BRIDGE_CONTRACT_BASE_LATEST` | Base bridge address | Required |
| `PRIVATE_KEY` | Test wallet private key | Required |
| `SOLVER_URL` | Solver service URL | `http://localhost:3001` |
| `RELAYER_URL` | Relayer service URL | `http://localhost:3000` |
| `AUCTION_DURATION` | Auction duration in seconds | `20` |
| `TEST_TIMEOUT` | Overall test timeout | `300000` (5 min) |

### Customizing Test Parameters

Edit `complete_flow_test.go` to modify:

```go
// Change test token amounts
expectedAmountB := utils.ParseEther("95")  // 95 tokens
reward := utils.ParseEther("5")            // 5 token reward

// Change bid amount
bidAmount := utils.ParseEther("98")        // Bid 98 tokens

// Change recipient distribution
amounts := []*big.Int{
  utils.ParseEther("30"),  // Recipient 1
  utils.ParseEther("35"),  // Recipient 2
  utils.ParseEther("30"),  // Recipient 3
}
```

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: E2E Tests

on: [push, pull_request]

jobs:
  e2e-tests:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v2

      - name: Set up Go
        uses: actions/setup-go@v2
        with:
          go-version: 1.21

      - name: Install dependencies
        run: go mod download

      - name: Start Relayer
        run: ./bin/relayer &
        working-directory: tachyon-prod

      - name: Start Solver
        run: ./bin/solver &
        working-directory: tachyon-prod

      - name: Run E2E Tests
        run: ./run_test.sh
        working-directory: tachyon-prod/test/e2e
        env:
          PRIVATE_KEY: ${{ secrets.TEST_PRIVATE_KEY }}
```

## Utilities Reference

### Helper Functions

Located in `utils/helpers.go`:

```go
// Create test wallet
wallet := CreateTestWallet(privateKeyHex)

// Initialize clients
clients := InitClients(ctx, config)

// Get transaction options
auth := GetTransactOpts(ctx, client, wallet, chainID)

// Wait for transaction
WaitForTransaction(ctx, client, txHash, timeout)

// Generate random address
addr := GenerateRandomAddress()

// Parse ether amount
wei := ParseEther("10.5")  // 10.5 ETH to wei

// Format wei to ether
eth := FormatEther(weiAmount)

// Sleep with progress
SleepWithProgress(30*time.Second, "Waiting for auction")
```

## Comparison with TypeScript Version

| Feature | TypeScript | Golang | Status |
|---------|-----------|--------|--------|
| Token Deployment | ✅ Full | ⚠️ Via scripts | Compatible |
| Intent Creation | ✅ | ✅ | Identical |
| Bidding | ✅ | ✅ | Identical |
| Solving | ✅ | ✅ | Identical |
| Settlement | ✅ zkVerify | ✅ Basic | Simplified |
| API Calls | ✅ | ✅ | Compatible |

## Known Limitations

1. **Token Deployment**: Currently requires pre-deployed tokens or TypeScript scripts
2. **Gas Optimization**: Fixed gas limits, not dynamically estimated
3. **Error Recovery**: Limited automatic retry logic
4. **Parallel Testing**: Single test execution only

## Future Enhancements

- [ ] Add parallel test execution
- [ ] Implement token deployment in Go
- [ ] Add performance benchmarks
- [ ] Create test fixtures for faster setup
- [ ] Add failure scenario tests
- [ ] Implement automatic retry logic
- [ ] Add detailed logging levels
- [ ] Create test report generation

## Contributing

When adding new tests:

1. Follow the existing structure
2. Add clear documentation
3. Include error handling
4. Add progress indicators
5. Update this README

## Getting Help

1. Check service logs
2. Review test output carefully
3. Verify environment configuration
4. Check network connectivity
5. Consult main project README

---

**Happy Testing! 🚀**

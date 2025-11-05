# E2E Test Execution Guide

This guide will walk you through testing the complete bridge flow using the Go implementation.

## Prerequisites ✅

Based on your confirmation:
- ✅ Contracts are deployed on Horizen Testnet and Base Sepolia
- ✅ Test wallet is funded on both networks
- ✅ Environment variables configured from relayer/env.example

## Quick Test Execution

### Step 1: Configure Test Environment

```bash
cd tachyon-prod/test/e2e

# Copy environment configuration
cp .env.test.example .env.test

# Edit with your deployed contract addresses
nano .env.test
```

Update `.env.test` with values from `relayer/env.example`:

```bash
# From relayer/env.example
HORIZEN_TESTNET_RPC_URL=https://horizen-rpc-testnet.appchain.base.org
BASE_SEPOLIA_RPC_URL=https://sepolia.base.org

# Your deployed contract addresses
BRIDGE_CONTRACT_HORIZEN_LATEST=0x8d692017aEA872De988AC27FfD6B9Fed3FF0FC13
BRIDGE_CONTRACT_BASE_LATEST=0xd2C8C5C6DAD1be31077b0EeDEb78fcB62f7e1066

# Your funded test wallet
PRIVATE_KEY=0x1dd0046b5493fff5b112fc1efea1d309d35e04eee398f851ac9f9a2434658f22

# Service URLs (default)
SOLVER_URL=http://localhost:3001
RELAYER_URL=http://localhost:3000
```

### Step 2: Start Services

**Terminal 1 - Relayer:**
```bash
cd tachyon-prod
./bin/relayer

# Or if not built:
go run ./cmd/relayer
```

Expected output:
```
INFO[...] Starting Tachyon Relayer Service...
INFO[...] Relayer initialized successfully
INFO[...] Starting HTTP server address=:3000
```

**Terminal 2 - Solver:**
```bash
cd tachyon-prod
./bin/solver

# Or if not built:
go run ./cmd/solver
```

Expected output:
```
INFO[...] Starting Tachyon Solver Service...
INFO[...] Solver initialized successfully
INFO[...] Starting event polling network=horizen
INFO[...] Starting HTTP server address=:3001
```

### Step 3: Verify Services Are Running

**Terminal 3:**
```bash
# Check relayer
curl http://localhost:3000/health

# Expected response:
# {"status":"healthy","service":"bridge-relayer","timestamp":"..."}

# Check solver
curl http://localhost:3001/health

# Expected response:
# {"status":"healthy","service":"bridge-solver","timestamp":"..."}
```

### Step 4: Run E2E Test

```bash
cd tachyon-prod/test/e2e

# Make run script executable (if needed)
chmod +x run_test.sh

# Run the test
./run_test.sh
```

## Alternative: Run Test Directly

```bash
cd tachyon-prod/test/e2e

# Build and run
go build -o e2e_test complete_flow_test.go
./e2e_test

# Or run directly
go run complete_flow_test.go
```

## What the Test Does

The E2E test will execute these steps automatically:

### 1. Service Health Check ✅
- Verifies relayer is responding
- Verifies solver is responding

### 2. Token Setup 🪙
- Uses existing token contracts or needs token deployment
- **Note**: May require using TypeScript token deployment first

### 3. Intent Creation 🎯
```
- Creates bridge intent on Horizen
- Target amount: 95 tokens
- Reward: 5 tokens
- Auction duration: 20 seconds
```

### 4. Recipient Storage 📋
```
- Generates 3 random recipient addresses
- Splits amount: 30, 35, 30 tokens
- Stores in relayer via POST /store-recipients
```

### 5. Bidding Process 🏆
```
- Places bid of 98 tokens
- Waits for auction to end (20 seconds)
- Finalizes auction
```

### 6. Token Deposit 💰
```
- Approves tokens for bridge contract
- Deposits tokens to pick up user funds
```

### 7. Solve on Destination Chain 🌐
```
- Approves TokenB on Base
- Calls solveIntentOnChain2
- Distributes to 3 recipients
```

### 8. Verification & Settlement ✅
```
- Verifies intent is solved on Base
- Calls relayer /settle endpoint
- Completes bridge flow
```

## Expected Test Output

```bash
🚀 Tachyon-Prod E2E Test Runner
================================

✅ Found .env.test configuration

🏥 Checking service health...
✅ Relayer service is running
✅ Solver service is running

📦 Building test...
-------------------
✅ Test built successfully

🧪 Running E2E Test...
=====================

🚀 Starting complete cross-chain bridge flow test (Go implementation)...

📍 Chain IDs: Horizen=845320009, Base=84532
👛 Wallet Address: 0x93dC72De59c169AA07b23F8D487021e15C57776E

🏥 Checking service health...
✅ Solver service is healthy
✅ Relayer service is healthy

🎯 Target Intent ID: [next ID] (will be created next)

🪙 STEP 1: Deploying test tokens...
[Token deployment or using existing tokens]

📋 STEP 1.5: Generating recipients and storing in relayer...
🎯 Recipients: [0x..., 0x..., 0x...]
💰 Amounts: [30, 35, 30] ETH
✅ Recipients stored in relayer

🎯 STEP 2: Creating intent on Horizen Testnet...
✅ Approved tokens for intent creation
✅ Intent created on Horizen
🎯 Actual Intent ID: [ID]
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
✅ Intent [ID] solved status: true

💰 STEP 9: Calling relayer to settle...
✅ Settlement transaction: 0x...
✅ Intent settled successfully!

🎉 Complete flow test finished successfully!
🎯 Final Intent ID used: [ID]

================================
✅ E2E Test PASSED

🎉 All tests completed successfully!
```

## Troubleshooting

### Issue: Token Deployment Error

**Error:**
```
❌ Failed to deploy TokenA: deploy via TypeScript scripts first
```

**Solution:**
The test currently expects tokens to be deployed using the original TypeScript deployment scripts. Either:

1. Deploy tokens using existing scripts:
   ```bash
   cd ../../scripts
   npx hardhat run deployTokens.ts --network horizen
   npx hardhat run deployTokens.ts --network base
   ```

2. Or use existing token addresses from env.example:
   ```bash
   TOKEN_A_HORIZEN=0x6d5be3853E3b277f9Cd2de7B911e4318eE78cfCb
   TOKEN_B_BASE=0xAb50d4703484B4F0079607c873E064b18F49D48e
   ```

3. Update test to use these addresses instead of deploying

### Issue: Services Not Running

**Error:**
```
❌ Relayer service is not responding
```

**Solution:**
```bash
# Check if services are running
ps aux | grep relayer
ps aux | grep solver

# Start services if needed
cd tachyon-prod
./bin/relayer &
./bin/solver &
```

### Issue: Insufficient Balance

**Error:**
```
❌ Failed to approve tokens: insufficient funds for gas
```

**Solution:**
1. Check wallet balance:
   ```bash
   curl http://localhost:3001/status
   ```

2. Get testnet funds:
   - Horizen: Use testnet faucet
   - Base Sepolia: https://faucet.base.org

### Issue: Contract Not Found

**Error:**
```
❌ Failed to create intent: contract not found
```

**Solution:**
Verify contract addresses in `.env.test`:
```bash
# Check on block explorer
# Horizen: https://horizen-explorer-testnet.appchain.base.org/address/0x8d692017aEA872De988AC27FfD6B9Fed3FF0FC13
# Base: https://sepolia-explorer.base.org/address/0xd2C8C5C6DAD1be31077b0EeDEb78fcB62f7e1066
```

### Issue: Transaction Timeout

**Error:**
```
Failed to wait for transaction: transaction wait timeout
```

**Solution:**
1. Check RPC connectivity
2. Increase timeout in code
3. Try alternative RPC endpoint
4. Check network status

## Test Customization

### Using Existing Tokens

To skip token deployment and use existing tokens, modify `complete_flow_test.go`:

```go
// Instead of deploying:
// tokenAAddr, err := deployMockERC20(...)

// Use existing addresses:
tokenAAddr := common.HexToAddress("0x6d5be3853E3b277f9Cd2de7B911e4318eE78cfCb")
tokenBAddr := common.HexToAddress("0xAb50d4703484B4F0079607c873E064b18F49D48e")
```

### Adjusting Test Parameters

Edit test constants:

```go
// Change auction duration
auctionDuration := int64(60) // 60 seconds instead of 20

// Change amounts
expectedAmountB := utils.ParseEther("100") // 100 tokens
reward := utils.ParseEther("10")           // 10 token reward
bidAmount := utils.ParseEther("105")       // Bid 105 tokens
```

## Verifying Test Results

### Check Transaction on Explorers

**Horizen Testnet:**
```
https://horizen-explorer-testnet.appchain.base.org/tx/[TX_HASH]
```

**Base Sepolia:**
```
https://sepolia-explorer.base.org/tx/[TX_HASH]
```

### Check Intent Status via API

```bash
# Get active intents
curl http://localhost:3001/active-intents

# Get specific intent status
curl http://localhost:3001/intent-status/[INTENT_ID]/horizen
```

### Check Relayer Storage

```bash
# List stored intents
curl http://localhost:3000/list-stored-intents

# Get recipients for intent
curl http://localhost:3000/get-recipients/[INTENT_ID]
```

## Success Criteria

The test is successful if:

- ✅ All 9 steps complete without errors
- ✅ Intent is created on Horizen
- ✅ Bid is placed and auction finalized
- ✅ Tokens are deposited
- ✅ Intent is solved on Base
- ✅ Verification returns `solved: true`
- ✅ Settlement transaction is submitted
- ✅ Final message shows "🎉 Complete flow test finished successfully!"

## Next Steps After Successful Test

1. **Monitor Services**: Watch solver and relayer logs for processing
2. **Check Balances**: Verify token transfers occurred correctly
3. **Repeat Testing**: Run multiple tests to ensure consistency
4. **Production Setup**: Use proper key management and monitoring
5. **Optimize Parameters**: Tune auction duration, profit margins, etc.

## Getting Help

If you encounter issues:

1. Check service logs (relayer and solver terminals)
2. Review test output carefully
3. Verify all prerequisites are met
4. Check environment configuration
5. Consult test/e2e/README.md for detailed troubleshooting

---

**Ready to test! 🚀**

Run: `cd tachyon-prod/test/e2e && ./run_test.sh`

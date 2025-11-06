import { ethers } from "hardhat";
import axios from "axios";
import dotenv from "dotenv";

dotenv.config({ path: "tachyon-prod/.env" });

const RELAYER_URL = 'http://localhost:3000';
const EXPECTED_AMOUNT_B = ethers.parseEther("95");

// Network configurations from .env
const networks = {
  horizen: {
    name: "Horizen Testnet",
    rpc: process.env.PAIR_1_SOURCE_RPC!,
    bridge: process.env.PAIR_1_SOURCE_BRIDGE!,
    explorer: process.env.PAIR_1_SOURCE_EXPLORER!,
    chainId: process.env.PAIR_1_SOURCE_CHAIN_ID!,
    tokens: {
      usdc: process.env.TOKEN_USDC_HORIZEN!,
      weth: process.env.TOKEN_WETH_HORIZEN!,
    }
  },
  base: {
    name: "Base Sepolia",
    rpc: process.env.PAIR_1_DEST_RPC!,
    bridge: process.env.PAIR_1_DEST_BRIDGE!,
    explorer: process.env.PAIR_1_DEST_EXPLORER!,
    chainId: process.env.PAIR_1_DEST_CHAIN_ID!,
    tokens: {
      usdc: process.env.TOKEN_USDC_BASE!,
      weth: process.env.TOKEN_WETH_BASE!,
    }
  },
  sepolia: {
    name: "Ethereum Sepolia",
    rpc: process.env.PAIR_2_DEST_RPC!,
    bridge: process.env.PAIR_2_DEST_BRIDGE!,
    explorer: process.env.PAIR_2_DEST_EXPLORER!,
    chainId: process.env.PAIR_2_DEST_CHAIN_ID!,
    tokens: {
      usdc: process.env.TOKEN_USDC_SEPOLIA!,
      weth: process.env.TOKEN_WETH_SEPOLIA!,
    }
  }
};

async function createIntent(
  sourceChain: string,
  destChain: string,
  sourceProvider: ethers.JsonRpcProvider,
  sourceBridge: any,
  tokenA: string,
  tokenB: string,
  testNumber: number
) {
  console.log(`\n${"=".repeat(80)}`);
  console.log(`🧪 TEST ${testNumber}: ${sourceChain} → ${destChain}`);
  console.log(`${"=".repeat(80)}`);

  const auctionDuration = 30; // 30 seconds for testing
  const reward = ethers.parseEther("5");

  // Get current latest intent ID
  const currentLatestIntentId = await sourceBridge.getLatestIntentId();
  const targetIntentId = currentLatestIntentId + 1n;
  console.log(`🎯 Target Intent ID: ${targetIntentId}`);

  // Get token contracts
  const MockERC20Factory = await ethers.getContractFactory("MockERC20");
  const tokenAContract = MockERC20Factory.attach(tokenA).connect(sourceBridge.runner);

  // Approve tokens for intent creation
  console.log("🔐 Approving tokens for intent creation...");
  const approveIntentTx = await tokenAContract.approve(
    await sourceBridge.getAddress(),
    EXPECTED_AMOUNT_B + reward
  );
  await approveIntentTx.wait();
  console.log("✅ Tokens approved");

  // Create intent
  console.log("🎯 Creating intent...");
  const createIntentTx = await sourceBridge.createIntent(
    tokenA,
    tokenB,
    EXPECTED_AMOUNT_B,
    EXPECTED_AMOUNT_B,
    reward,
    auctionDuration
  );
  await createIntentTx.wait();

  const actualIntentId = await sourceBridge.getLatestIntentId();
  console.log(`✅ Intent created: ${actualIntentId}`);
  console.log(`📍 Transaction: ${(networks as any)[sourceChain].explorer}/tx/${createIntentTx.hash}`);

  // Generate recipients for this intent
  const recipient1 = ethers.Wallet.createRandom().address;
  const recipient2 = ethers.Wallet.createRandom().address;
  const recipient3 = ethers.Wallet.createRandom().address;

  const amount1 = ethers.parseEther("30");
  const amount2 = ethers.parseEther("35");
  const amount3 = ethers.parseEther("30");

  const testRecipients = [recipient1, recipient2, recipient3];
  const testAmounts = [amount1, amount2, amount3];

  // Store recipient information in relayer
  // IMPORTANT: Use SOURCE chain ID, not destination, because intent IDs are per-chain
  console.log("📋 Storing recipients in relayer...");
  try {
    const sourceChainId = (networks as any)[sourceChain].chainId;
    await axios.post(`${RELAYER_URL}/store-recipients`, {
      intentId: actualIntentId.toString(),
      recipients: testRecipients,
      amounts: testAmounts.map(a => a.toString()),
      chainId: Number(sourceChainId)
    });
    console.log("✅ Recipients stored in relayer");
  } catch (error: any) {
    console.log(`❌ Error storing recipients: ${error.message}`);
  }

  console.log(`\n⏳ Waiting for solver to detect and process intent...`);
  console.log(`   (Auction duration: ${auctionDuration}s + processing time)`);

  return {
    intentId: actualIntentId,
    recipients: testRecipients,
    amounts: testAmounts,
    sourceChain,
    destChain
  };
}

async function main() {
  console.log("🚀 Starting Multi-Chain E2E Test");
  console.log("Testing 4 bidirectional intent flows:\n");
  console.log("  1. Horizen → Base Sepolia");
  console.log("  2. Base Sepolia → Horizen");
  console.log("  3. Horizen → Ethereum Sepolia");
  console.log("  4. Ethereum Sepolia → Horizen\n");

  // Setup providers
  const horizenProvider = new ethers.JsonRpcProvider(networks.horizen.rpc);
  const baseProvider = new ethers.JsonRpcProvider(networks.base.rpc);
  const sepoliaProvider = new ethers.JsonRpcProvider(networks.sepolia.rpc);

  // Setup wallets
  const privateKey = process.env.PRIVATE_KEY!;
  const horizenWallet = new ethers.Wallet(privateKey, horizenProvider);
  const baseWallet = new ethers.Wallet(privateKey, baseProvider);
  const sepoliaWallet = new ethers.Wallet(privateKey, sepoliaProvider);

  // Connect to bridge contracts
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  const bridge_Horizen = BridgeFactory.attach(networks.horizen.bridge).connect(horizenWallet);
  const bridge_Base = BridgeFactory.attach(networks.base.bridge).connect(baseWallet);
  const bridge_Sepolia = BridgeFactory.attach(networks.sepolia.bridge).connect(sepoliaWallet);

  console.log("💼 Wallet Address:", horizenWallet.address);
  console.log("\n📡 Network Status:");
  console.log(`  Horizen: Block ${await horizenProvider.getBlockNumber()}`);
  console.log(`  Base Sepolia: Block ${await baseProvider.getBlockNumber()}`);
  console.log(`  Ethereum Sepolia: Block ${await sepoliaProvider.getBlockNumber()}`);

  // Create all 4 intents
  const intents = [];

  // Test 1: Horizen → Base Sepolia (WETH)
  intents.push(await createIntent(
    "horizen",
    "base",
    horizenProvider,
    bridge_Horizen,
    networks.horizen.tokens.weth,
    networks.base.tokens.weth,
    1
  ));

  // Wait between intents
  console.log("\n⏳ Waiting 5 seconds before next intent...");
  await new Promise(resolve => setTimeout(resolve, 5000));

  // Test 2: Base Sepolia → Horizen (USDC)
  intents.push(await createIntent(
    "base",
    "horizen",
    baseProvider,
    bridge_Base,
    networks.base.tokens.usdc,
    networks.horizen.tokens.usdc,
    2
  ));

  // Wait between intents
  console.log("\n⏳ Waiting 5 seconds before next intent...");
  await new Promise(resolve => setTimeout(resolve, 5000));

  // Test 3: Horizen → Ethereum Sepolia (USDC)
  intents.push(await createIntent(
    "horizen",
    "sepolia",
    horizenProvider,
    bridge_Horizen,
    networks.horizen.tokens.usdc,
    networks.sepolia.tokens.usdc,
    3
  ));

  // Wait between intents
  console.log("\n⏳ Waiting 5 seconds before next intent...");
  await new Promise(resolve => setTimeout(resolve, 5000));

  // Test 4: Ethereum Sepolia → Horizen (WETH)
  intents.push(await createIntent(
    "sepolia",
    "horizen",
    sepoliaProvider,
    bridge_Sepolia,
    networks.sepolia.tokens.weth,
    networks.horizen.tokens.weth,
    4
  ));

  // Wait for all auctions to complete and solver to process
  console.log(`\n\n${"=".repeat(80)}`);
  console.log("⏳ All intents created! Waiting 60 seconds for solver to process...");
  console.log(`${"=".repeat(80)}`);
  await new Promise(resolve => setTimeout(resolve, 60000));

  // Verify all intents
  console.log(`\n\n${"=".repeat(80)}`);
  console.log("🔍 VERIFICATION PHASE");
  console.log(`${"=".repeat(80)}`);

  for (let i = 0; i < intents.length; i++) {
    const intent = intents[i];
    console.log(`\n📊 Intent ${i + 1}: ${intent.sourceChain} → ${intent.destChain} (ID: ${intent.intentId})`);

    try {
      // Get source and dest bridges
      const sourceBridge = intent.sourceChain === "horizen" ? bridge_Horizen :
                          intent.sourceChain === "base" ? bridge_Base : bridge_Sepolia;
      const destBridge = intent.destChain === "horizen" ? bridge_Horizen :
                        intent.destChain === "base" ? bridge_Base : bridge_Sepolia;

      // Check if intent completed on source
      const intentDetails = await sourceBridge.getIntent(intent.intentId);
      console.log(`  Source Chain Status: ${intentDetails.completed ? "✅ Completed" : "❌ Not Completed"}`);

      if (intentDetails.winningSolver !== ethers.ZeroAddress) {
        console.log(`  Winning Solver: ${intentDetails.winningSolver}`);
        console.log(`  Winning Bid: ${ethers.formatEther(intentDetails.winningBid)} ETH`);
      }

      // Check if solved on destination
      const isSolved = await destBridge.isIntentSolvedOnChain2(intent.intentId);
      console.log(`  Destination Chain Status: ${isSolved ? "✅ Solved" : "❌ Not Solved"}`);

      // Test verify endpoint
      const destChainId = (networks as any)[intent.destChain].chainId;
      const verifyResponse = await axios.post(`${RELAYER_URL}/verify`, {
        intentId: intent.intentId.toString(),
        chain2IntentId: intent.intentId.toString(),
        chainId: Number(destChainId)
      });
      console.log(`  Relayer Verification: ${verifyResponse.data.verified ? "✅ Verified" : "❌ Not Verified"}`);

    } catch (error: any) {
      console.log(`  ❌ Error verifying intent: ${error.message}`);
    }
  }

  console.log(`\n\n${"=".repeat(80)}`);
  console.log("🎉 Multi-Chain E2E Test Complete!");
  console.log(`${"=".repeat(80)}`);
  console.log("\n📊 Summary:");
  console.log(`  Total Intents Created: ${intents.length}`);
  console.log(`  Test Scenarios:`);
  console.log(`    ✓ Horizen → Base Sepolia (WETH)`);
  console.log(`    ✓ Base Sepolia → Horizen (USDC)`);
  console.log(`    ✓ Horizen → Ethereum Sepolia (USDC)`);
  console.log(`    ✓ Ethereum Sepolia → Horizen (WETH)`);
  console.log(`\n💡 Check the solver logs to see the routing decisions for each chain pair!`);
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Test failed:", error);
    process.exit(1);
  });

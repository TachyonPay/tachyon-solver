import { ethers } from "hardhat";

async function main() {
  console.log("🚀 Deploying BridgeIntent & Test Tokens to Rari Testnet");
  console.log("=========================================================");

  const network = {
    name: "Rari Testnet",
    rpcUrl: process.env.RARI_TESTNET_RPC_URL || "https://rari-testnet.calderachain.xyz/http",
    chainId: 1918988905,
    explorer: "https://rari-testnet.calderachain.xyz"
  };

  const privateKey = process.env.PRIVATE_KEY!;

  // Create provider and wallet
  const provider = new ethers.JsonRpcProvider(network.rpcUrl);
  const wallet = new ethers.Wallet(privateKey, provider);
  let nonce = await provider.getTransactionCount(wallet.address);

  console.log(`\n📡 ${network.name}:`);
  console.log(`   Chain ID: ${network.chainId}`);
  console.log(`   RPC: ${network.rpcUrl}`);
  console.log(`   Block: ${await provider.getBlockNumber()}`);
  console.log(`   Balance: ${ethers.formatEther(await provider.getBalance(wallet.address))} ETH`);
  console.log(`   Nonce: ${nonce}`);
  console.log(`\n💼 Wallet Address: ${wallet.address}`);

  // Deploy BridgeIntent contract
  console.log("\n🏗️  Deploying BridgeIntent contract...");
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  
  const bridge = await (BridgeFactory as any).connect(wallet).deploy(
    network.chainId,
    { nonce: nonce++ }
  );
  await bridge.waitForDeployment();
  const bridgeAddress = await bridge.getAddress();
  console.log(`✅ BridgeIntent deployed: ${bridgeAddress}`);

  // Deploy test tokens (USDC, WETH, ZEN)
  console.log("\n🪙 Deploying Test Tokens (USDC, WETH, ZEN)...");
  const MockERC20Factory = await ethers.getContractFactory("MockERC20");
  
  const tokens: { [key: string]: string } = {};

  const tokenConfigs = [
    { key: "usdc", name: "USD Coin", symbol: "USDC", decimals: 6 },
    { key: "weth", name: "Wrapped Ether", symbol: "WETH", decimals: 18 },
    { key: "zen", name: "Horizen", symbol: "ZEN", decimals: 18 }
  ];

  for (const config of tokenConfigs) {
    const initialSupply = config.decimals === 6
      ? ethers.parseUnits("1000000000", 6) // 1B USDC
      : ethers.parseEther("1000000000");    // 1B WETH/ZEN

    const token = await MockERC20Factory.connect(wallet).deploy(
      config.name,
      config.symbol,
      config.decimals,
      initialSupply,
      { nonce: nonce++ }
    );
    await token.waitForDeployment();
    tokens[config.key] = await token.getAddress();
    console.log(`   ✅ ${config.symbol}: ${tokens[config.key]}`);
  }

  // Add relayer
  console.log("\n⚙️  Setting up relayer...");
  const tx = await bridge.addRelayer(wallet.address, { nonce: nonce++ });
  await tx.wait();
  console.log(`✅ Relayer added: ${wallet.address}`);

  // Generate configuration output
  console.log("\n\n📋 DEPLOYMENT SUMMARY");
  console.log("=".repeat(80));
  console.log(`\n🔗 Bridge Contract: ${bridgeAddress}`);
  console.log(`\n🪙 Token Addresses:`);
  console.log(`   USDC: ${tokens.usdc}`);
  console.log(`   WETH: ${tokens.weth}`);
  console.log(`   ZEN:  ${tokens.zen}`);

  console.log("\n\n📝 .ENV CONFIGURATION FOR RARI");
  console.log("=".repeat(80));
  console.log("\nAdd the following to your .env file:\n");

  const envConfig = `# Chain 4: Rari Testnet
CHAIN_4_ID=1918988905
CHAIN_4_NAME=rari
CHAIN_4_RPC=https://rari-testnet.calderachain.xyz/http
CHAIN_4_BRIDGE=${bridgeAddress}
CHAIN_4_EXPLORER=https://rari-testnet.calderachain.xyz

# Rari Token Addresses
TOKEN_USDC_RARI=${tokens.usdc}
TOKEN_WETH_RARI=${tokens.weth}
TOKEN_ZEN_RARI=${tokens.zen}`;

  console.log(envConfig);

  console.log("\n\n⚠️  IMPORTANT: Update CHAIN_COUNT=4 in your .env file!");

  console.log("\n\n🎉 RARI TESTNET DEPLOYMENT COMPLETED!");
  console.log("=".repeat(80));
  console.log(`\n🔗 Explorer: ${network.explorer}/address/${bridgeAddress}`);
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Deployment failed:", error);
    process.exit(1);
  });

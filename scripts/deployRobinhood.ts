import { ethers } from "hardhat";

async function main() {
  console.log("🚀 Deploying BridgeIntent & Test Tokens to Robinhood Testnet");
  console.log("=============================================================");

  const provider = new ethers.JsonRpcProvider("https://rpc.testnet.chain.robinhood.com/rpc");
  const privateKey = process.env.PRIVATE_KEY!;
  const wallet = new ethers.Wallet(privateKey, provider);

  const balance = await provider.getBalance(wallet.address);
  let nonce = await provider.getTransactionCount(wallet.address);

  console.log(`📡 Robinhood Testnet:`);
  console.log(`   Block: ${await provider.getBlockNumber()}`);
  console.log(`   Balance: ${ethers.formatEther(balance)} ETH`);
  console.log(`   Wallet: ${wallet.address}`);
  console.log(`   Nonce: ${nonce}`);

  // Deploy BridgeIntent
  console.log("\n🏗️  Deploying BridgeIntent...");
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  const bridge = await (BridgeFactory as any).connect(wallet).deploy(46630, { nonce: nonce++ });
  await bridge.waitForDeployment();
  const bridgeAddr = await bridge.getAddress();
  console.log(`✅ BridgeIntent: ${bridgeAddr}`);

  // Add relayer
  console.log("\n⚙️  Adding relayer...");
  const addRelayerTx = await bridge.addRelayer(wallet.address, { nonce: nonce++ });
  await addRelayerTx.wait();
  console.log(`✅ Relayer added: ${wallet.address}`);

  // Deploy tokens
  console.log("\n🪙 Deploying Test Tokens...");
  const MockERC20Factory = await ethers.getContractFactory("MockERC20");

  const tokenConfigs = [
    { key: "usdc", name: "USD Coin", symbol: "USDC", decimals: 6, supply: ethers.parseUnits("1000000000", 6) },
    { key: "weth", name: "Wrapped Ether", symbol: "WETH", decimals: 18, supply: ethers.parseEther("1000000000") },
    { key: "zen", name: "Horizen", symbol: "ZEN", decimals: 18, supply: ethers.parseEther("1000000000") },
    { key: "tsla", name: "Tesla", symbol: "TSLA", decimals: 18, supply: ethers.parseEther("1000000000") },
    { key: "amzn", name: "Amazon", symbol: "AMZN", decimals: 18, supply: ethers.parseEther("1000000000") },
  ];

  const tokens: Record<string, string> = {};
  for (const config of tokenConfigs) {
    const token = await MockERC20Factory.connect(wallet).deploy(
      config.name, config.symbol, config.decimals, config.supply,
      { nonce: nonce++ }
    );
    await token.waitForDeployment();
    tokens[config.key] = await token.getAddress();
    console.log(`   ✅ ${config.symbol}: ${tokens[config.key]}`);
  }

  // Summary
  console.log("\n\n📋 DEPLOYMENT SUMMARY");
  console.log("=".repeat(60));
  console.log(`\nBridgeIntent: ${bridgeAddr}`);
  console.log(`USDC:         ${tokens.usdc}`);
  console.log(`WETH:         ${tokens.weth}`);
  console.log(`ZEN:          ${tokens.zen}`);

  console.log("\n\n📝 .ENV CONFIG (add to your .env):");
  console.log(`
# Chain: Robinhood Testnet
CHAIN_X_ID=46630
CHAIN_X_NAME=robinhood
CHAIN_X_RPC=https://rpc.testnet.chain.robinhood.com/rpc
CHAIN_X_BRIDGE=${bridgeAddr}
CHAIN_X_EXPLORER=https://explorer.testnet.chain.robinhood.com

TOKEN_USDC_ROBINHOOD=${tokens.usdc}
TOKEN_WETH_ROBINHOOD=${tokens.weth}
TOKEN_ZEN_ROBINHOOD=${tokens.zen}
TOKEN_TSLA_ROBINHOOD=${tokens.tsla}
TOKEN_AMZN_ROBINHOOD=${tokens.amzn}
`);

  console.log("🎉 DEPLOYMENT COMPLETED!");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Deployment failed:", error);
    process.exit(1);
  });

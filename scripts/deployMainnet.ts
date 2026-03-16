import { ethers } from "hardhat";
import * as fs from "fs";
import * as path from "path";

async function main() {
  console.log("🚀 Deploying BridgeIntent V1 & Mock USDC to Mainnet Chains");
  console.log("============================================================");

  const networks = {
    horizen: {
      name: "Horizen Mainnet",
      rpcUrl: process.env.HORIZEN_MAINNET_RPC_URL || "https://horizen.calderachain.xyz/http",
      chainId: 26514,
      explorer: "https://horizen.calderaexplorer.xyz",
    },
    base: {
      name: "Base Mainnet",
      rpcUrl: process.env.BASE_MAINNET_RPC_URL || "https://1rpc.io/base",
      chainId: 8453,
      explorer: "https://basescan.org",
    },
  };

  const privateKey = process.env.PRIVATE_KEY!;
  if (!privateKey) {
    throw new Error("PRIVATE_KEY not set");
  }

  const providers: any = {};
  const wallets: any = {};
  const nonces: any = {};

  for (const [key, network] of Object.entries(networks)) {
    providers[key] = new ethers.JsonRpcProvider(network.rpcUrl);
    wallets[key] = new ethers.Wallet(privateKey, providers[key]);
    nonces[key] = await providers[key].getTransactionCount(wallets[key].address);

    console.log(`\n📡 ${network.name}:`);
    console.log(`   Block: ${await providers[key].getBlockNumber()}`);
    console.log(`   Balance: ${ethers.formatEther(await providers[key].getBalance(wallets[key].address))} ETH`);
    console.log(`   Nonce: ${nonces[key]}`);
  }

  const deployerAddr = wallets.horizen.address;
  console.log(`\n💼 Deployer/Relayer/Solver Address: ${deployerAddr}`);

  // Deploy BridgeIntent contracts
  console.log("\n🏗️  Deploying BridgeIntent contracts...");
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  const bridges: any = {};

  for (const [key, network] of Object.entries(networks)) {
    console.log(`\nDeploying BridgeIntent on ${network.name} (chainId: ${network.chainId})...`);
    const bridge = await (BridgeFactory as any).connect(wallets[key]).deploy(
      network.chainId,
      { nonce: nonces[key]++ }
    );
    await bridge.waitForDeployment();
    bridges[key] = bridge;
    console.log(`✅ ${network.name}: ${await bridge.getAddress()}`);
  }

  // Deploy Mock USDC on both chains
  console.log("\n🪙 Deploying Mock USDC (6 decimals, 1M supply)...");
  const MockERC20Factory = await ethers.getContractFactory("MockERC20");
  const usdcAddresses: any = {};
  const initialSupply = ethers.parseUnits("1000000", 6); // 1M USDC

  for (const [key, network] of Object.entries(networks)) {
    console.log(`\nDeploying USDC on ${network.name}...`);
    const token = await MockERC20Factory.connect(wallets[key]).deploy(
      "USD Coin",
      "USDC",
      6,
      initialSupply,
      { nonce: nonces[key]++ }
    );
    await token.waitForDeployment();
    usdcAddresses[key] = await token.getAddress();
    console.log(`✅ USDC on ${network.name}: ${usdcAddresses[key]}`);
  }

  // Add deployer as relayer on both chains
  console.log("\n⚙️  Adding deployer as relayer...");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`Adding relayer on ${network.name}...`);
    const tx = await bridges[key].addRelayer(deployerAddr, { nonce: nonces[key]++ });
    await tx.wait();
    console.log(`✅ Relayer added on ${network.name}`);
  }

  // Summary
  console.log("\n\n📋 DEPLOYMENT SUMMARY");
  console.log("=".repeat(80));

  console.log("\n🔗 Bridge Contracts:");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`  ${network.name.padEnd(20)} ${await bridges[key].getAddress()}`);
  }

  console.log("\n🪙 USDC Addresses:");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`  ${network.name.padEnd(20)} ${usdcAddresses[key]}`);
  }

  console.log("\n👤 Deployer/Relayer/Solver:");
  console.log(`  ${deployerAddr}`);

  // Update tachyon-prod/.env — append as Chain 3 & 4, update CHAIN_COUNT
  const envPath = path.join(__dirname, "..", "tachyon-prod", ".env");
  console.log(`\n\n📝 Updating ${envPath}...`);

  let envContent = fs.readFileSync(envPath, "utf-8");

  // Update CHAIN_COUNT from 2 to 4
  envContent = envContent.replace(/^CHAIN_COUNT=\d+$/m, "CHAIN_COUNT=4");

  // Append mainnet chains and tokens
  const mainnetConfig = `
# Chain 3: Horizen Mainnet
CHAIN_3_ID=26514
CHAIN_3_NAME=horizen_mainnet
CHAIN_3_RPC=https://horizen.calderachain.xyz/http
CHAIN_3_BRIDGE=${await bridges.horizen.getAddress()}
CHAIN_3_EXPLORER=https://horizen.calderaexplorer.xyz

# Chain 4: Base Mainnet
CHAIN_4_ID=8453
CHAIN_4_NAME=base_mainnet
CHAIN_4_RPC=https://1rpc.io/base
CHAIN_4_BRIDGE=${await bridges.base.getAddress()}
CHAIN_4_EXPLORER=https://basescan.org

# Mainnet Token Addresses
TOKEN_USDC_HORIZEN_MAINNET=${usdcAddresses.horizen}
TOKEN_USDC_BASE_MAINNET=${usdcAddresses.base}
`;

  envContent += mainnetConfig;
  fs.writeFileSync(envPath, envContent);
  console.log("✅ .env updated with Chain 3 (Horizen Mainnet) & Chain 4 (Base Mainnet)");

  // Also write a standalone deployment JSON for reference
  const deploymentInfo = {
    timestamp: new Date().toISOString(),
    deployer: deployerAddr,
    chains: {
      horizen_mainnet: {
        chainId: 26514,
        bridge: await bridges.horizen.getAddress(),
        usdc: usdcAddresses.horizen,
        rpc: "https://horizen.calderachain.xyz/http",
        explorer: "https://horizen.calderaexplorer.xyz",
      },
      base_mainnet: {
        chainId: 8453,
        bridge: await bridges.base.getAddress(),
        usdc: usdcAddresses.base,
        rpc: "https://1rpc.io/base",
        explorer: "https://basescan.org",
      },
    },
  };

  const deploymentPath = path.join(__dirname, "..", "deployments", "mainnet.json");
  fs.mkdirSync(path.dirname(deploymentPath), { recursive: true });
  fs.writeFileSync(deploymentPath, JSON.stringify(deploymentInfo, null, 2));
  console.log(`✅ Deployment info saved to ${deploymentPath}`);

  console.log("\n🎉 MAINNET DEPLOYMENT COMPLETED!");
  console.log("=".repeat(80));
  console.log("\n🔗 Explorer Links:");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`  ${network.name}: ${network.explorer}/address/${deployerAddr}`);
  }
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Deployment failed:", error);
    process.exit(1);
  });

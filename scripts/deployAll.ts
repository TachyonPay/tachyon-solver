import { ethers } from "hardhat";

async function main() {
  console.log("🚀 Deploying BridgeIntent & Test Tokens to All Chains");
  console.log("====================================================");

  // Network configurations
  const networks = {
    horizen: {
      name: "Horizen Testnet",
      rpcUrl: process.env.HORIZEN_TESTNET_RPC_URL || "https://horizen-rpc-testnet.appchain.base.org",
      chainId: 845320009,
      explorer: "https://horizen-explorer-testnet.appchain.base.org"
    },
    base: {
      name: "Base Sepolia",
      rpcUrl: process.env.BASE_SEPOLIA_RPC_URL || "https://sepolia.base.org",
      chainId: 84532,
      explorer: "https://sepolia-explorer.base.org"
    },
    sepolia: {
      name: "Ethereum Sepolia",
      rpcUrl: process.env.SEPOLIA_RPC_URL || "https://ethereum-sepolia.rpc.subquery.network/public",
      chainId: 11155111,
      explorer: "https://sepolia.etherscan.io"
    }
  };

  const privateKey = process.env.PRIVATE_KEY!;

  // Create providers and wallets for all networks
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

  console.log(`\n💼 Wallet Address: ${wallets.horizen.address}`);

  // Deploy BridgeIntent contracts
  console.log("\n🏗️  Deploying BridgeIntent contracts...");
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  const bridges: any = {};

  for (const [key, network] of Object.entries(networks)) {
    console.log(`\nDeploying BridgeIntent on ${network.name}...`);
    const bridge = await (BridgeFactory as any).connect(wallets[key]).deploy(
      network.chainId,
      { nonce: nonces[key]++ }
    );
    await bridge.waitForDeployment();
    bridges[key] = bridge;
    console.log(`✅ ${network.name}: ${await bridge.getAddress()}`);
  }

  // Deploy test tokens (USDC, WETH, ZEN) on all chains
  console.log("\n🪙 Deploying Test Tokens (USDC, WETH, ZEN)...");
  const MockERC20Factory = await ethers.getContractFactory("MockERC20");
  const tokens: any = {
    usdc: {},
    weth: {},
    zen: {}
  };

  const tokenConfigs = [
    { key: "usdc", name: "USD Coin", symbol: "USDC", decimals: 6 },
    { key: "weth", name: "Wrapped Ether", symbol: "WETH", decimals: 18 },
    { key: "zen", name: "Horizen", symbol: "ZEN", decimals: 18 }
  ];

  for (const [chainKey, network] of Object.entries(networks)) {
    console.log(`\n📦 Deploying tokens on ${network.name}...`);

    for (const config of tokenConfigs) {
      const initialSupply = config.decimals === 6
        ? ethers.parseUnits("1000000000", 6) // 1B USDC
        : ethers.parseEther("1000000000");    // 1B WETH/ZEN

      const token = await MockERC20Factory.connect(wallets[chainKey]).deploy(
        config.name,
        config.symbol,
        config.decimals,
        initialSupply,
        { nonce: nonces[chainKey]++ }
      );
      await token.waitForDeployment();
      tokens[config.key][chainKey] = await token.getAddress();
      console.log(`   ✅ ${config.symbol}: ${tokens[config.key][chainKey]}`);
    }
  }

  // Add relayers
  console.log("\n⚙️  Setting up relayers...");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`Adding relayer on ${network.name}...`);
    const tx = await bridges[key].addRelayer(wallets[key].address, { nonce: nonces[key]++ });
    await tx.wait();
    console.log(`✅ Relayer added on ${network.name}`);
  }

  // Generate .env configuration
  console.log("\n\n📋 DEPLOYMENT SUMMARY");
  console.log("=".repeat(80));
  console.log("\n🔗 Bridge Contracts:");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`${network.name.padEnd(30)} ${await bridges[key].getAddress()}`);
  }

  console.log("\n🪙 Token Addresses:");
  for (const config of tokenConfigs) {
    console.log(`\n${config.symbol}:`);
    for (const [chainKey, network] of Object.entries(networks)) {
      console.log(`  ${network.name.padEnd(28)} ${tokens[config.key][chainKey]}`);
    }
  }

  console.log("\n\n📝 .ENV CONFIGURATION");
  console.log("=".repeat(80));
  console.log("\nCopy the following into your .env file:\n");

  const envConfig = `# Chain-Based Configuration (NEW ARCHITECTURE)
# Configure each chain the solver supports
# The solver will automatically handle any source→destination if both chains are configured

# Chain Configuration
CHAIN_COUNT=3

# Chain 1: Horizen Testnet
CHAIN_1_ID=845320009
CHAIN_1_NAME=horizen
CHAIN_1_RPC=https://horizen-rpc-testnet.appchain.base.org
CHAIN_1_BRIDGE=${await bridges.horizen.getAddress()}
CHAIN_1_EXPLORER=https://horizen-explorer-testnet.appchain.base.org

# Chain 2: Base Sepolia
CHAIN_2_ID=84532
CHAIN_2_NAME=base
CHAIN_2_RPC=https://sepolia.base.org
CHAIN_2_BRIDGE=${await bridges.base.getAddress()}
CHAIN_2_EXPLORER=https://sepolia-explorer.base.org

# Chain 3: Ethereum Sepolia
CHAIN_3_ID=11155111
CHAIN_3_NAME=sepolia
CHAIN_3_RPC=https://ethereum-sepolia.rpc.subquery.network/public
CHAIN_3_BRIDGE=${await bridges.sepolia.getAddress()}
CHAIN_3_EXPLORER=https://sepolia.etherscan.io

# Token Addresses
# USDC (6 decimals)
TOKEN_USDC_HORIZEN=${tokens.usdc.horizen}
TOKEN_USDC_BASE=${tokens.usdc.base}
TOKEN_USDC_SEPOLIA=${tokens.usdc.sepolia}

# WETH (18 decimals)
TOKEN_WETH_HORIZEN=${tokens.weth.horizen}
TOKEN_WETH_BASE=${tokens.weth.base}
TOKEN_WETH_SEPOLIA=${tokens.weth.sepolia}

# ZEN (18 decimals)
TOKEN_ZEN_HORIZEN=${tokens.zen.horizen}
TOKEN_ZEN_BASE=${tokens.zen.base}
TOKEN_ZEN_SEPOLIA=${tokens.zen.sepolia}

# Wallet Configuration
PRIVATE_KEY=${privateKey}

# Solver Configuration
MIN_PROFIT_MARGIN=0
MAX_BID_AMOUNT=1000000000000000000000
BID_INCREMENT=1000000000000000000
BALANCE_BUFFER=0.1

# Service Configuration
RELAYER_URL=http://localhost:3000
SOLVER_PORT=3001
RELAYER_PORT=3000
`;

  console.log(envConfig);

  console.log("\n\n🎉 DEPLOYMENT COMPLETED!");
  console.log("=".repeat(80));
  console.log("\n✨ Next Steps:");
  console.log("1. Copy the .env configuration above into your .env file");
  console.log("2. Update CHAIN_COUNT to add/remove chains as needed");
  console.log("3. Build solver and relayer: cd tachyon-prod && make build-solver && make build-relayer");
  console.log("4. Start the relayer: cd tachyon-prod && ./bin/relayer");
  console.log("5. Start the solver: cd tachyon-prod && ./bin/solver");
  console.log("\n🔗 Explorer Links:");
  for (const [key, network] of Object.entries(networks)) {
    console.log(`${network.name}: ${network.explorer}/address/${wallets[key].address}`);
  }
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Deployment failed:", error);
    process.exit(1);
  });

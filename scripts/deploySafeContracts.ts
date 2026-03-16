/**
 * Deploy Safe v1.3.0 contracts on Horizen Testnet
 *
 * The canonical Safe deployer (0x914d7Fec6aaC8cd542e72Bca78B30650d45643d7)
 * is already on Horizen, but the canonical contract addresses are not.
 * This script deploys GnosisSafe singleton, ProxyFactory, and
 * CompatibilityFallbackHandler using our wallet, then prints the addresses
 * to update safe.go with.
 */

import { ethers } from "hardhat";

const RPC_URL = process.env.SAFE_DEPLOY_RPC || "https://horizen-rpc-testnet.appchain.base.org";

// Canonical addresses (for checking — these are what we want to match)
const CANONICAL_SINGLETON        = "0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552";
const CANONICAL_PROXY_FACTORY    = "0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2";
const CANONICAL_FALLBACK_HANDLER = "0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4";

async function main() {
  const provider = new ethers.JsonRpcProvider(RPC_URL);
  const privateKey = process.env.PRIVATE_KEY;
  if (!privateKey) throw new Error("PRIVATE_KEY not set");

  const wallet = new ethers.Wallet(privateKey, provider);
  const balance = await provider.getBalance(wallet.address);

  console.log("🔐 Deployer:", wallet.address);
  console.log("💰 Balance:", ethers.formatEther(balance), "ETH");
  console.log("🔗 Chain:", (await provider.getNetwork()).chainId.toString());
  console.log("");

  // Check what's already deployed
  const [scCode, pfCode, fbCode] = await Promise.all([
    provider.getCode(CANONICAL_SINGLETON),
    provider.getCode(CANONICAL_PROXY_FACTORY),
    provider.getCode(CANONICAL_FALLBACK_HANDLER),
  ]);

  console.log("📋 Canonical address status on Horizen:");
  console.log(`   GnosisSafe singleton:    ${CANONICAL_SINGLETON} ${scCode !== "0x" ? "✅" : "❌"}`);
  console.log(`   ProxyFactory:            ${CANONICAL_PROXY_FACTORY} ${pfCode !== "0x" ? "✅" : "❌"}`);
  console.log(`   FallbackHandler:         ${CANONICAL_FALLBACK_HANDLER} ${fbCode !== "0x" ? "✅" : "❌"}`);
  console.log("");

  if (scCode !== "0x" && pfCode !== "0x" && fbCode !== "0x") {
    console.log("🎉 All Safe contracts already deployed at canonical addresses!");
    return;
  }

  if (ethers.parseEther("0.005") > balance) {
    throw new Error("Insufficient balance — need at least 0.005 ETH for deployment");
  }

  // Load artifacts from @gnosis.pm/safe-contracts
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const safeArtifact = require("@gnosis.pm/safe-contracts/build/artifacts/contracts/GnosisSafe.sol/GnosisSafe.json");
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const factoryArtifact = require("@gnosis.pm/safe-contracts/build/artifacts/contracts/proxies/GnosisSafeProxyFactory.sol/GnosisSafeProxyFactory.json");
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const fallbackArtifact = require("@gnosis.pm/safe-contracts/build/artifacts/contracts/handler/CompatibilityFallbackHandler.sol/CompatibilityFallbackHandler.json");

  let nonce = await provider.getTransactionCount(wallet.address);

  // 1. Deploy GnosisSafe singleton
  let singletonAddr: string;
  if (scCode !== "0x") {
    singletonAddr = CANONICAL_SINGLETON;
    console.log("✅ Singleton already deployed, skipping");
  } else {
    console.log("1️⃣  Deploying GnosisSafe singleton...");
    const factory = new ethers.ContractFactory(safeArtifact.abi, safeArtifact.bytecode, wallet);
    const contract = await factory.deploy({ nonce: nonce++, gasLimit: 6000000 });
    await contract.waitForDeployment();
    singletonAddr = await contract.getAddress();
    console.log(`   ✅ GnosisSafe: ${singletonAddr}`);
  }

  // 2. Deploy ProxyFactory
  let proxyFactoryAddr: string;
  if (pfCode !== "0x") {
    proxyFactoryAddr = CANONICAL_PROXY_FACTORY;
    console.log("✅ ProxyFactory already deployed, skipping");
  } else {
    console.log("2️⃣  Deploying GnosisSafeProxyFactory...");
    const factory = new ethers.ContractFactory(factoryArtifact.abi, factoryArtifact.bytecode, wallet);
    const contract = await factory.deploy({ nonce: nonce++, gasLimit: 3000000 });
    await contract.waitForDeployment();
    proxyFactoryAddr = await contract.getAddress();
    console.log(`   ✅ ProxyFactory: ${proxyFactoryAddr}`);
  }

  // 3. Deploy CompatibilityFallbackHandler
  let fallbackHandlerAddr: string;
  if (fbCode !== "0x") {
    fallbackHandlerAddr = CANONICAL_FALLBACK_HANDLER;
    console.log("✅ FallbackHandler already deployed, skipping");
  } else {
    console.log("3️⃣  Deploying CompatibilityFallbackHandler...");
    const factory = new ethers.ContractFactory(fallbackArtifact.abi, fallbackArtifact.bytecode, wallet);
    const contract = await factory.deploy({ nonce: nonce++, gasLimit: 2000000 });
    await contract.waitForDeployment();
    fallbackHandlerAddr = await contract.getAddress();
    console.log(`   ✅ FallbackHandler: ${fallbackHandlerAddr}`);
  }

  const singletonChanged    = singletonAddr    !== CANONICAL_SINGLETON;
  const factoryChanged      = proxyFactoryAddr !== CANONICAL_PROXY_FACTORY;
  const fallbackChanged     = fallbackHandlerAddr !== CANONICAL_FALLBACK_HANDLER;

  console.log("\n🎉 Deployment complete!");
  console.log("=".repeat(70));

  if (singletonChanged || factoryChanged || fallbackChanged) {
    console.log("\n⚠️  Addresses differ from canonical (expected for non-mainnet chains).");
    console.log("   Update tachyon-prod/pkg/blockchain/safe.go:\n");
    console.log(`var (`);
    console.log(`\tSafeSingletonAddress    = common.HexToAddress("${singletonAddr}")`);
    console.log(`\tSafeProxyFactoryAddress = common.HexToAddress("${proxyFactoryAddr}")`);
    console.log(`)`);
    console.log(`\n// In DeploySafe / PredictSafeAddressForOwner:`);
    console.log(`fallbackHandler := common.HexToAddress("${fallbackHandlerAddr}")`);
  } else {
    console.log("\n✅ All contracts deployed at canonical addresses.");
    console.log("   safe.go does not need updating.");
  }
}

main()
  .then(() => process.exit(0))
  .catch((err) => {
    console.error("❌", err.message || err);
    process.exit(1);
  });

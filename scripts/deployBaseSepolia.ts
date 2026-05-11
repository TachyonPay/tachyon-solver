// Redeploys the updated BridgeIntent contract to Base Sepolia ONLY.
// Use this when Horizen already has the new contract (with createIntentToSolana)
// but Base Sepolia is still running the old version.
//
//   npx hardhat run scripts/deployBaseSepolia.ts --network base_sepolia
//
// After it prints the new address:
//   1. Update CHAIN_2_BRIDGE in tachyon-prod/.env to the new address.
//   2. Restart relayer + solver.
//
// Existing mock USDC at TOKEN_USDC_BASE keeps working — no token redeploy.

import { ethers } from "hardhat";

async function main() {
  console.log("🚀 Deploying BridgeIntent to Base Sepolia (only)");
  console.log("================================================");

  const [deployer] = await ethers.getSigners();
  const network = await ethers.provider.getNetwork();

  if (network.chainId !== 84532n) {
    console.error(`❌ Wrong network — got chainId ${network.chainId}, expected 84532. Pass --network base_sepolia.`);
    process.exit(1);
  }

  console.log(`Network    : Base Sepolia (chainId=${network.chainId})`);
  console.log(`Deployer   : ${deployer.address}`);
  const balance = await ethers.provider.getBalance(deployer.address);
  console.log(`Balance    : ${ethers.formatEther(balance)} ETH`);
  console.log();

  if (balance < ethers.parseEther("0.005")) {
    console.error("❌ Deployer balance too low. Need at least 0.005 ETH on Base Sepolia.");
    process.exit(1);
  }

  console.log("🏗️  Deploying BridgeIntent...");
  const BridgeFactory = await ethers.getContractFactory("BridgeIntent");
  const bridge = await (BridgeFactory as any).deploy(network.chainId);
  await bridge.waitForDeployment();
  const bridgeAddr = await bridge.getAddress();
  console.log(`✅ BridgeIntent deployed at: ${bridgeAddr}`);
  console.log();

  // Sanity check: verify createIntentToSolana exists.
  if (typeof (bridge as any).createIntentToSolana !== "function") {
    console.warn("⚠️  Deployed contract doesn't expose createIntentToSolana — wrong source?");
  } else {
    console.log("✓ Contract exposes createIntentToSolana — Solana flow is supported.");
  }

  // Add deployer as authorized relayer (matches the existing pattern in deploy.ts).
  console.log(`⚙️  Adding ${deployer.address} as authorized relayer...`);
  const relayerTx = await (bridge as any).addRelayer(deployer.address);
  await relayerTx.wait();
  console.log("✅ Relayer authorized");
  console.log();

  console.log("═════════════════════════════════════════════════════════");
  console.log("✨ DONE");
  console.log("═════════════════════════════════════════════════════════");
  console.log();
  console.log("Update tachyon-prod/.env:");
  console.log(`  CHAIN_2_BRIDGE=${bridgeAddr}`);
  console.log();
  console.log(`Verify on basescan:`);
  console.log(`  https://sepolia.basescan.org/address/${bridgeAddr}#readContract`);
  console.log();
  console.log("Then restart the relayer + solver and re-run base_solana_flow.go.");
}

main()
  .then(() => process.exit(0))
  .catch((err) => {
    console.error("❌ Deploy failed:", err);
    process.exit(1);
  });

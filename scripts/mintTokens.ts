import { ethers } from "hardhat";
import dotenv from "dotenv";

dotenv.config({ path: "tachyon-prod/.env" });

async function main() {
  console.log("🪙 Minting Test Tokens for Solver");
  console.log("=".repeat(80));

  const solverAddress = "0x93dC72De59c169AA07b23F8D487021e15C57776E";

  // Amount to mint: 10M tokens (with 18 decimals for WETH/ZEN, 6 for USDC)
  const amountWETH = ethers.parseEther("10000000");  // 10M tokens
  const amountUSDC = ethers.parseUnits("10000000", 6); // 10M USDC
  const amountZEN = ethers.parseEther("10000000");   // 10M ZEN

  const networks = {
    horizen: {
      name: "Horizen Testnet",
      rpc: process.env.PAIR_1_SOURCE_RPC!,
      tokens: {
        usdc: process.env.TOKEN_USDC_HORIZEN!,
        weth: process.env.TOKEN_WETH_HORIZEN!,
        zen: process.env.TOKEN_ZEN_HORIZEN!
      }
    },
    base: {
      name: "Base Sepolia",
      rpc: process.env.PAIR_1_DEST_RPC!,
      tokens: {
        usdc: process.env.TOKEN_USDC_BASE!,
        weth: process.env.TOKEN_WETH_BASE!,
        zen: process.env.TOKEN_ZEN_BASE!
      }
    },
    sepolia: {
      name: "Ethereum Sepolia",
      rpc: process.env.PAIR_2_DEST_RPC!,
      tokens: {
        usdc: process.env.TOKEN_USDC_SEPOLIA!,
        weth: process.env.TOKEN_WETH_SEPOLIA!,
        zen: process.env.TOKEN_ZEN_SEPOLIA!
      }
    }
  };

  const privateKey = process.env.PRIVATE_KEY!;

  for (const [chainKey, network] of Object.entries(networks)) {
    console.log(`\n🔗 ${network.name}`);
    console.log("-".repeat(80));

    const provider = new ethers.JsonRpcProvider(network.rpc);
    const wallet = new ethers.Wallet(privateKey, provider);

    // USDC (6 decimals)
    try {
      const usdcToken = await ethers.getContractAt("MockERC20", network.tokens.usdc, wallet);
      const tx = await usdcToken["mint(address,uint256)"](solverAddress, amountUSDC);
      await tx.wait();
      console.log(`✅ USDC: Minted 10M tokens to solver`);
    } catch (error: any) {
      console.log(`❌ USDC: ${error.message}`);
    }

    // WETH (18 decimals)
    try {
      const wethToken = await ethers.getContractAt("MockERC20", network.tokens.weth, wallet);
      const tx = await wethToken["mint(address,uint256)"](solverAddress, amountWETH);
      await tx.wait();
      console.log(`✅ WETH: Minted 10M tokens to solver`);
    } catch (error: any) {
      console.log(`❌ WETH: ${error.message}`);
    }

    // ZEN (18 decimals)
    try {
      const zenToken = await ethers.getContractAt("MockERC20", network.tokens.zen, wallet);
      const tx = await zenToken["mint(address,uint256)"](solverAddress, amountZEN);
      await tx.wait();
      console.log(`✅ ZEN: Minted 10M tokens to solver`);
    } catch (error: any) {
      console.log(`❌ ZEN: ${error.message}`);
    }
  }

  console.log("\n\n🎉 Token minting completed!");
  console.log("=".repeat(80));
  console.log(`\nSolver address: ${solverAddress}`);
  console.log(`Tokens minted: 10M USDC, 10M WETH, 10M ZEN on each chain`);
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ Minting failed:", error);
    process.exit(1);
  });

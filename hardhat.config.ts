import { HardhatUserConfig } from "hardhat/config";
import "@nomicfoundation/hardhat-toolbox";
import dotenv from "dotenv";

// Load environment variables from .env file
dotenv.config();

const config: HardhatUserConfig = {
  solidity: {
    version: "0.8.28",
    settings: {
      optimizer: {
        enabled: true,
        runs: 200,
      },
      viaIR: true, // Enable IR-based compilation to avoid stack too deep errors
    },
  },
  networks: {
    hardhat: {
      // Default Hardhat network
    },
    horizen_testnet: {
      url: process.env.HORIZEN_TESTNET_RPC_URL || "https://horizen-rpc-testnet.appchain.base.org",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 845320009,
      gasPrice: "auto",
    },
    base_sepolia: {
      url: process.env.BASE_SEPOLIA_RPC_URL || "https://sepolia.base.org",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 84532,
      gasPrice: "auto",
    },
    sepolia: {
      url: process.env.SEPOLIA_RPC_URL || "https://ethereum-sepolia.rpc.subquery.network/public",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 11155111,
      gasPrice: "auto",
    },
    arbitrum_sepolia: {
      url: process.env.ARBITRUM_SEPOLIA_RPC_URL || "https://arbitrum-sepolia.gateway.tenderly.co",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 421614,
      gasPrice: "auto",
    },
    rari_testnet: {
      url: process.env.RARI_TESTNET_RPC_URL || "https://rari-testnet.calderachain.xyz/http",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 1918988905,
      gasPrice: "auto",
    },
    goerli: {
      url: process.env.GOERLI_RPC_URL || "",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 5,
    },
    mainnet: {
      url: process.env.MAINNET_RPC_URL || "",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 1,
    },
    horizen_mainnet: {
      url: process.env.HORIZEN_MAINNET_RPC_URL || "https://horizen.calderachain.xyz/http",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 26514,
      gasPrice: "auto",
    },
    base_mainnet: {
      url: process.env.BASE_MAINNET_RPC_URL || "https://1rpc.io/base",
      accounts: process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [],
      chainId: 8453,
      gasPrice: "auto",
    },
  },
  etherscan: {
    apiKey: {
      mainnet: process.env.ETHERSCAN_API_KEY || "",
      sepolia: process.env.ETHERSCAN_API_KEY || "",
      goerli: process.env.ETHERSCAN_API_KEY || "",
      horizen_testnet: "not-needed", // Custom network, may not need API key
      base_sepolia: process.env.BASESCAN_API_KEY || "not-needed",
      arbitrum_sepolia: process.env.ARBISCAN_API_KEY || "not-needed",
      rari_testnet: "not-needed"
    },
    customChains: [
      {
        network: "horizen_testnet",
        chainId: 845320009,
        urls: {
          apiURL: "https://horizen-explorer-testnet.appchain.base.org/api",
          browserURL: "https://horizen-explorer-testnet.appchain.base.org/"
        }
      },
      {
        network: "base_sepolia",
        chainId: 84532,
        urls: {
          apiURL: "https://sepolia-explorer.base.org/api",
          browserURL: "https://sepolia-explorer.base.org/"
        }
      },
      {
        network: "arbitrum_sepolia",
        chainId: 421614,
        urls: {
          apiURL: "https://api-sepolia.arbiscan.io/api",
          browserURL: "https://sepolia.arbiscan.io/"
        }
      },
      {
        network: "rari_testnet",
        chainId: 1918988905,
        urls: {
          apiURL: "https://rari-testnet.calderachain.xyz/api",
          browserURL: "https://rari-testnet.calderachain.xyz/"
        }
      }
    ]
  },
  gasReporter: {
    enabled: process.env.REPORT_GAS !== undefined,
    currency: "USD",
  },
};

export default config;

#!/bin/bash

# AWS Console Manual Deployment Script
# Run this script directly on your EC2 instance after SSH connection
# Designed for Amazon Linux 2023

set -e

echo "=========================================="
echo "Tachyon Manual AWS Deployment"
echo "For EC2 instances via AWS Console"
echo "=========================================="
echo ""

# Check if running on Amazon Linux
if [ ! -f /etc/os-release ]; then
    echo "ERROR: Cannot detect OS version"
    exit 1
fi

source /etc/os-release
if [[ "$ID" != "amzn" ]]; then
    echo "WARNING: This script is designed for Amazon Linux 2023"
    echo "Current OS: $PRETTY_NAME"
    read -p "Continue anyway? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

echo "Step 1: System Update"
echo "----------------------------------------"
sudo dnf update -y
echo "✓ System updated"
echo ""

echo "Step 2: Install Go"
echo "----------------------------------------"
if command -v go &> /dev/null; then
    echo "Go is already installed: $(go version)"
else
    GO_VERSION="1.23.0"
    echo "Installing Go ${GO_VERSION}..."
    wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
    rm go${GO_VERSION}.linux-amd64.tar.gz

    # Update PATH for current session
    export PATH=$PATH:/usr/local/go/bin
    export GOPATH=$HOME/go

    # Update PATH permanently
    if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
        echo 'export GOPATH=$HOME/go' >> ~/.bashrc
    fi

    echo "✓ Go installed: $(go version)"
fi
echo ""

echo "Step 3: Install Required Tools"
echo "----------------------------------------"
sudo dnf install -y git screen net-tools curl
echo "✓ Tools installed (git, screen, net-tools, curl)"
echo ""

echo "Step 4: Project Setup"
echo "----------------------------------------"
PROJECT_DIR="$HOME/zk-tachyon-solver"

if [ -d "$PROJECT_DIR" ]; then
    echo "Project directory exists at: $PROJECT_DIR"
    read -p "Do you want to pull latest changes? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        cd "$PROJECT_DIR"
        git pull
        echo "✓ Project updated"
    fi
else
    echo "Project directory not found."
    echo "Cloning Tachyon repository from GitHub..."
    REPO_URL="https://github.com/TachyonPay/zk-tachyon-solver.git"

    git clone "$REPO_URL" "$PROJECT_DIR"
    echo "✓ Project cloned from $REPO_URL"
fi
echo ""

cd "$PROJECT_DIR/tachyon-prod"

echo "Step 5: Environment Configuration"
echo "----------------------------------------"
if [ -f ".env" ]; then
    echo "✓ .env file already exists"
    echo "Current configuration preview:"
    grep -E "^(CHAIN_COUNT|PRIVATE_KEY|RELAYER_PORT|SOLVER_PORT)" .env || echo "  (no key variables found)"
    echo ""
    read -p "Do you want to edit .env? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        nano .env
    fi
else
    echo "Creating .env file..."
    cat > .env << 'EOF'
# Chain-Based Configuration (NEW ARCHITECTURE)
# Configure each chain the solver supports
# The solver will automatically handle any source→destination if both chains are configured

# Chain Configuration
CHAIN_COUNT=3

# Chain 1: Horizen Testnet
CHAIN_1_ID=845320009
CHAIN_1_NAME=horizen
CHAIN_1_RPC=https://horizen-rpc-testnet.appchain.base.org
CHAIN_1_BRIDGE=0xc862b8DC344FAf35CE0931c44A222a09cA2Bde33
CHAIN_1_EXPLORER=https://horizen-explorer-testnet.appchain.base.org

# Chain 2: Base Sepolia
CHAIN_2_ID=84532
CHAIN_2_NAME=base
CHAIN_2_RPC=https://base-sepolia.g.alchemy.com/v2/ofHyxuC9Jqq9wA2EbVVlN7wtdmuCTHXZ
CHAIN_2_BRIDGE=0x1628Ae09a5f49b8A6B3Ec4Ca20c4C4a40017e13D
CHAIN_2_EXPLORER=https://sepolia-explorer.base.org

# Chain 3: Ethereum Sepolia
CHAIN_3_ID=11155111
CHAIN_3_NAME=sepolia
CHAIN_3_RPC=https://eth-sepolia.g.alchemy.com/v2/ofHyxuC9Jqq9wA2EbVVlN7wtdmuCTHXZ
CHAIN_3_BRIDGE=0xF3462eB059E0CdA7f20D451Bb2A53Aed937fEfca
CHAIN_3_EXPLORER=https://sepolia.etherscan.io

# Token Addresses
# USDC (6 decimals)
TOKEN_USDC_HORIZEN=0xe3fA8D680BDa33dD128a7CCE2e291A39C264975c
TOKEN_USDC_BASE=0x39b2DfD00D262B7A8d83468C244901c2c1252256
TOKEN_USDC_SEPOLIA=0x49463867BcA4Ba59B6602B7A32bDF5B469B644fa

# WETH (18 decimals)
TOKEN_WETH_HORIZEN=0x5D5567B92a1d9E9A53Db80DB9D0B570CB0f2C8d4
TOKEN_WETH_BASE=0x04FAD9adBB9aE63b305aE3c91B027D095487A9e2
TOKEN_WETH_SEPOLIA=0x33E38B8BFfc065110902485932F5417e6cF911A0

# ZEN (18 decimals)
TOKEN_ZEN_HORIZEN=0x10FFc3D770344e1AcCF0bFf351a7D0f3b14153ec
TOKEN_ZEN_BASE=0xE991595dfd6A4b8f9C308cA3a9d469bF76FcA7Da
TOKEN_ZEN_SEPOLIA=0xdB822B0dDBEE4b1dDd536bF1D654631D71765c7E

# Wallet Configuration
PRIVATE_KEY=0x1dd0046b5493fff5b112fc1efea1d309d35e04eee398f851ac9f9a2434658f22

# ECIES Encryption Configuration
# Private key for ECIES encryption/decryption (hex-encoded 32 bytes)
# If not set, a new keypair will be generated on startup
# The relayer will print the generated key that you should add here

ECIES_PRIVATE_KEY=f35a3856e790c77d364291ff3b7841c675105a9efd6e3ada908b134c9ddd26ff
# Solver Configuration
MIN_PROFIT_MARGIN=0
MAX_BID_AMOUNT=1000000000000000000000
BID_INCREMENT=1000000000000000000
BALANCE_BUFFER=0.1

# Service Configuration
RELAYER_URL=http://localhost:3000
SOLVER_PORT=3001
RELAYER_PORT=3000

EOF

    chmod 600 .env
    echo "✓ .env template created"
    echo ""
    echo "IMPORTANT: You MUST edit the .env file with your actual values!"
    echo "Opening editor in 3 seconds..."
    sleep 3
    nano .env

    # Verify critical config
    if grep -q "your_ethereum_private_key" .env; then
        echo ""
        echo "WARNING: PRIVATE_KEY is still set to placeholder value!"
        read -p "Continue anyway? (y/n) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            echo "Please edit .env and run this script again"
            exit 1
        fi
    fi
fi
echo ""

echo "Step 6: Build Services"
echo "----------------------------------------"
echo "Downloading Go dependencies..."
go mod download
go mod tidy
echo ""

echo "Building relayer and solver..."
make clean
make build

if [ ! -f "bin/relayer" ] || [ ! -f "bin/solver" ]; then
    echo "ERROR: Build failed! Binaries not found."
    exit 1
fi

echo "✓ Build successful!"
echo "  - bin/relayer"
echo "  - bin/solver"
echo ""

echo "Step 7: Service Configuration"
echo "----------------------------------------"
read -p "Do you want to set up systemd services (recommended for production)? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Creating systemd service files..."

    # Relayer service
    sudo tee /etc/systemd/system/tachyon-relayer.service > /dev/null << EOF
[Unit]
Description=Tachyon Relayer Service
After=network.target

[Service]
Type=simple
User=$USER
WorkingDirectory=$PROJECT_DIR/tachyon-prod
Environment="PATH=/usr/local/go/bin:/usr/bin:/bin"
ExecStart=$PROJECT_DIR/tachyon-prod/bin/relayer
Restart=always
RestartSec=10
StandardOutput=append:$PROJECT_DIR/tachyon-prod/relayer.log
StandardError=append:$PROJECT_DIR/tachyon-prod/relayer.log

[Install]
WantedBy=multi-user.target
EOF

    # Solver service
    sudo tee /etc/systemd/system/tachyon-solver.service > /dev/null << EOF
[Unit]
Description=Tachyon Solver Service
After=network.target tachyon-relayer.service

[Service]
Type=simple
User=$USER
WorkingDirectory=$PROJECT_DIR/tachyon-prod
Environment="PATH=/usr/local/go/bin:/usr/bin:/bin"
ExecStart=$PROJECT_DIR/tachyon-prod/bin/solver
Restart=always
RestartSec=10
StandardOutput=append:$PROJECT_DIR/tachyon-prod/solver.log
StandardError=append:$PROJECT_DIR/tachyon-prod/solver.log

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable tachyon-relayer
    sudo systemctl enable tachyon-solver

    echo "✓ Systemd services configured"
    echo ""

    read -p "Start services now? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "Starting services..."
        sudo systemctl start tachyon-relayer
        sleep 2
        sudo systemctl start tachyon-solver
        sleep 2

        echo ""
        echo "Service Status:"
        sudo systemctl status tachyon-relayer --no-pager -l
        echo ""
        sudo systemctl status tachyon-solver --no-pager -l
    fi
else
    echo "Skipping systemd setup. You can start services manually using screen:"
    echo "  screen -S relayer -dm bash -c 'cd $PROJECT_DIR/tachyon-prod && ./bin/relayer'"
    echo "  screen -S solver -dm bash -c 'cd $PROJECT_DIR/tachyon-prod && ./bin/solver'"
fi
echo ""

echo "=========================================="
echo "Deployment Complete!"
echo "=========================================="
echo ""
echo "Important Information:"
echo "----------------------------------------"
echo "Service Ports:"
echo "  - Relayer: http://localhost:3000"
echo "  - Solver:  http://localhost:3001"
echo ""
echo "Test Commands:"
echo "  curl http://localhost:3000/health"
echo "  curl http://localhost:3001/status"
echo ""
echo "View Logs:"
echo "  sudo journalctl -u tachyon-relayer -f"
echo "  sudo journalctl -u tachyon-solver -f"
echo "  tail -f $PROJECT_DIR/tachyon-prod/relayer.log"
echo "  tail -f $PROJECT_DIR/tachyon-prod/solver.log"
echo ""
echo "Manage Services:"
echo "  sudo systemctl restart tachyon-relayer"
echo "  sudo systemctl restart tachyon-solver"
echo "  sudo systemctl stop tachyon-relayer tachyon-solver"
echo "  sudo systemctl status tachyon-relayer tachyon-solver"
echo ""
echo "Security Reminder:"
echo "  - Ensure AWS Security Group allows ports 3000, 3001"
echo "  - Keep your .env file secure (chmod 600 .env)"
echo "  - Never commit .env to version control"
echo ""
echo "Next Steps:"
echo "  1. Verify services are running"
echo "  2. Test endpoints with curl"
echo "  3. Monitor logs for any errors"
echo "  4. Set up external access (if needed)"
echo ""

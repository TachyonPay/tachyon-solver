#!/bin/bash

# AWS EC2 Setup Script for Tachyon Solver & Relayer
# Run this script on a fresh Amazon Linux 2023 instance

set -e

echo "=========================================="
echo "Tachyon AWS Deployment Setup"
echo "=========================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored messages
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [ "$EUID" -eq 0 ]; then
    print_error "Please do not run this script as root"
    exit 1
fi

# Update system
print_info "Updating system packages..."
sudo dnf update -y

# Install Go
print_info "Installing Go 1.23..."
GO_VERSION="1.23.0"
if ! command -v go &> /dev/null; then
    wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
    rm go${GO_VERSION}.linux-amd64.tar.gz

    # Add Go to PATH
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    echo 'export GOPATH=$HOME/go' >> ~/.bashrc
    export PATH=$PATH:/usr/local/go/bin
    export GOPATH=$HOME/go

    print_info "Go installed successfully"
else
    print_info "Go is already installed: $(go version)"
fi

# Install git
print_info "Installing git..."
sudo dnf install -y git

# Install screen for persistent sessions
print_info "Installing screen..."
sudo dnf install -y screen

# Install net-tools for netstat
print_info "Installing net-tools..."
sudo dnf install -y net-tools

# Create project directory
print_info "Setting up project directory..."
cd ~
PROJECT_DIR="$HOME/zk-tachyon-solver"

if [ ! -d "$PROJECT_DIR" ]; then
    print_warn "Project directory not found. Please clone your repository manually:"
    echo "  git clone <your-repo-url> $PROJECT_DIR"
    exit 1
fi

cd "$PROJECT_DIR/tachyon-prod"

# Check if .env exists
if [ ! -f ".env" ]; then
    print_warn ".env file not found!"
    print_info "Creating template .env file..."

    cat > .env << 'EOF'
# Chain Configurations
CHAIN_COUNT=3

# Chain 1 - Horizen (example)
CHAIN_1_ID=845320009
CHAIN_1_NAME=horizen
CHAIN_1_RPC=https://rpc-testnet.horizenlabs.io/ethv1
CHAIN_1_BRIDGE=0xYourBridgeAddress
CHAIN_1_EXPLORER=https://explorer-testnet.horizen.io

# Chain 2 - Base Sepolia (example)
CHAIN_2_ID=84532
CHAIN_2_NAME=base
CHAIN_2_RPC=https://base-sepolia.g.alchemy.com/v2/your-api-key
CHAIN_2_BRIDGE=0xYourBridgeAddress
CHAIN_2_EXPLORER=https://sepolia.basescan.org

# Chain 3 - Sepolia (example)
CHAIN_3_ID=11155111
CHAIN_3_NAME=sepolia
CHAIN_3_RPC=https://eth-sepolia.g.alchemy.com/v2/your-api-key
CHAIN_3_BRIDGE=0xYourBridgeAddress
CHAIN_3_EXPLORER=https://sepolia.etherscan.io

# Wallet Configuration (REQUIRED)
PRIVATE_KEY=your_ethereum_private_key_without_0x

# ECIES Encryption Key (will be auto-generated if not provided)
# ECIES_PRIVATE_KEY=

# Solver Configuration
MIN_PROFIT_MARGIN=0.02
MAX_BID_AMOUNT=1000000000000000000000
BID_INCREMENT=1000000000000000000
BALANCE_BUFFER=0.1

# Service Configuration
RELAYER_URL=http://localhost:3000
SOLVER_PORT=3001
RELAYER_PORT=3000
EOF

    chmod 600 .env
    print_warn "Please edit .env file with your actual configuration:"
    print_warn "  nano $PROJECT_DIR/tachyon-prod/.env"
    print_warn "After editing, run this script again or manually proceed with 'make build'"
    exit 0
fi

# Download dependencies
print_info "Downloading Go dependencies..."
go mod download
go mod tidy

# Build the project
print_info "Building relayer and solver..."
make build

# Verify binaries
if [ ! -f "bin/relayer" ] || [ ! -f "bin/solver" ]; then
    print_error "Build failed! Binaries not found."
    exit 1
fi

print_info "Build successful!"

# Create systemd service files
print_info "Creating systemd service files..."

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

# Reload systemd
print_info "Reloading systemd daemon..."
sudo systemctl daemon-reload

# Enable services
print_info "Enabling services to start on boot..."
sudo systemctl enable tachyon-relayer
sudo systemctl enable tachyon-solver

# Start services
print_info "Starting services..."
sudo systemctl start tachyon-relayer
sleep 3
sudo systemctl start tachyon-solver

# Check service status
print_info "Checking service status..."
sleep 2

RELAYER_STATUS=$(sudo systemctl is-active tachyon-relayer)
SOLVER_STATUS=$(sudo systemctl is-active tachyon-solver)

echo ""
echo "=========================================="
echo "Deployment Status"
echo "=========================================="

if [ "$RELAYER_STATUS" = "active" ]; then
    print_info "✓ Relayer service is running"
else
    print_error "✗ Relayer service failed to start"
    print_info "Check logs: sudo journalctl -u tachyon-relayer -n 50"
fi

if [ "$SOLVER_STATUS" = "active" ]; then
    print_info "✓ Solver service is running"
else
    print_error "✗ Solver service failed to start"
    print_info "Check logs: sudo journalctl -u tachyon-solver -n 50"
fi

echo ""
print_info "Useful commands:"
echo "  - View relayer logs:  sudo journalctl -u tachyon-relayer -f"
echo "  - View solver logs:   sudo journalctl -u tachyon-solver -f"
echo "  - Restart relayer:    sudo systemctl restart tachyon-relayer"
echo "  - Restart solver:     sudo systemctl restart tachyon-solver"
echo "  - Stop services:      sudo systemctl stop tachyon-relayer tachyon-solver"
echo "  - Check status:       sudo systemctl status tachyon-relayer tachyon-solver"
echo ""
print_info "Test endpoints:"
echo "  - curl http://localhost:3000/health"
echo "  - curl http://localhost:3001/status"
echo ""

print_info "Deployment complete!"

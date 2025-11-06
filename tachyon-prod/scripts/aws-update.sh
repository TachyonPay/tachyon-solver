#!/bin/bash

# AWS Update Script for Tachyon Services
# Run this on your EC2 instance to update to latest code

set -e

echo "=========================================="
echo "Tachyon Service Update Script"
echo "=========================================="
echo ""

PROJECT_DIR="$HOME/zk-tachyon-solver"

if [ ! -d "$PROJECT_DIR" ]; then
    echo "ERROR: Project directory not found at $PROJECT_DIR"
    exit 1
fi

cd "$PROJECT_DIR"

echo "Current status:"
echo "----------------------------------------"
git log -1 --oneline
echo ""

read -p "Do you want to update to latest version? (y/n) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Update cancelled"
    exit 0
fi

echo ""
echo "Step 1: Stopping services..."
echo "----------------------------------------"
sudo systemctl stop tachyon-solver
sudo systemctl stop tachyon-relayer
echo "✓ Services stopped"
echo ""

echo "Step 2: Pulling latest code..."
echo "----------------------------------------"
git fetch origin
git pull origin master
echo "✓ Code updated"
echo ""

echo "Latest commit:"
git log -1 --oneline
echo ""

echo "Step 3: Rebuilding services..."
echo "----------------------------------------"
cd tachyon-prod

# Clean old builds
make clean

# Download dependencies
go mod download
go mod tidy

# Build
make build

if [ ! -f "bin/relayer" ] || [ ! -f "bin/solver" ]; then
    echo "ERROR: Build failed!"
    exit 1
fi

echo "✓ Build successful"
echo ""

echo "Step 4: Restarting services..."
echo "----------------------------------------"
sudo systemctl start tachyon-relayer
sleep 2
sudo systemctl start tachyon-solver
sleep 2

echo "✓ Services restarted"
echo ""

echo "Step 5: Verifying services..."
echo "----------------------------------------"
RELAYER_STATUS=$(sudo systemctl is-active tachyon-relayer)
SOLVER_STATUS=$(sudo systemctl is-active tachyon-solver)

if [ "$RELAYER_STATUS" = "active" ]; then
    echo "✓ Relayer is running"
else
    echo "✗ Relayer failed to start"
    echo "Check logs: sudo journalctl -u tachyon-relayer -n 50"
fi

if [ "$SOLVER_STATUS" = "active" ]; then
    echo "✓ Solver is running"
else
    echo "✗ Solver failed to start"
    echo "Check logs: sudo journalctl -u tachyon-solver -n 50"
fi

echo ""
echo "=========================================="
echo "Update Complete!"
echo "=========================================="
echo ""
echo "Monitor logs:"
echo "  sudo journalctl -u tachyon-relayer -f"
echo "  sudo journalctl -u tachyon-solver -f"
echo ""

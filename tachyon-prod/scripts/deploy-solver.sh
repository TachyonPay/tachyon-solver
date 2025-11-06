#!/bin/bash

# Deploy Solver to EC2
# Usage: ./scripts/deploy-solver.sh [ssh-key-path]

set -e

EC2_HOST="ec2-user@65.0.45.215"
SSH_KEY="${1:-~/.ssh/your-key.pem}"
REMOTE_DIR="~/zk-tachyon-solver/tachyon-prod"

echo "🚀 Deploying Solver to EC2"
echo "=========================="
echo ""

# Check if SSH key exists
if [ ! -f "$SSH_KEY" ]; then
    echo "❌ SSH key not found: $SSH_KEY"
    echo "Usage: ./scripts/deploy-solver.sh [ssh-key-path]"
    exit 1
fi

# Build solver locally
echo "📦 Building solver locally..."
make build-solver
echo "✅ Solver built"
echo ""

# Stop remote solver
echo "🛑 Stopping remote solver..."
ssh -i "$SSH_KEY" "$EC2_HOST" "pkill -f './bin/solver' || true"
sleep 2
echo "✅ Solver stopped"
echo ""

# Copy binary to EC2
echo "📤 Copying solver binary to EC2..."
scp -i "$SSH_KEY" bin/solver "$EC2_HOST:$REMOTE_DIR/bin/"
echo "✅ Binary copied"
echo ""

# Start solver on EC2
echo "🚀 Starting solver on EC2..."
ssh -i "$SSH_KEY" "$EC2_HOST" "cd $REMOTE_DIR && nohup ./bin/solver > solver.log 2>&1 &"
sleep 3
echo "✅ Solver started"
echo ""

# Check if solver is running
echo "🔍 Checking solver status..."
ssh -i "$SSH_KEY" "$EC2_HOST" "pgrep -f './bin/solver' && echo '✅ Solver is running' || echo '❌ Solver failed to start'"
echo ""

echo "🎉 Deployment complete!"
echo ""
echo "To view logs:"
echo "  ssh -i $SSH_KEY $EC2_HOST 'tail -f $REMOTE_DIR/solver.log'"

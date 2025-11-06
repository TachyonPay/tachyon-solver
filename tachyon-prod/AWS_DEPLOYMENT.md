# AWS Deployment Guide for Tachyon Solver & Relayer

This guide will help you deploy the Tachyon Relayer and Solver services on AWS EC2 (Amazon Linux 2023).

## Prerequisites

1. AWS EC2 instance (Amazon Linux 2023) - Recommended: t3.medium or larger
2. Security group configured to allow:
   - Port 22 (SSH)
   - Port 3000 (Relayer)
   - Port 3001 (Solver)
   - Outbound internet access
3. Your private key file (.pem) for SSH access
4. Ethereum wallet private key for solver operations

## Architecture

- **Relayer Service**: Manages cross-chain intent settlement with privacy preservation (Port 3000)
- **Solver Service**: Monitors blockchain events and solves intents (Port 3001)
- Both services communicate via HTTP and interact with multiple blockchain networks

## Quick Start

### 1. Connect to Your EC2 Instance

```bash
ssh -i your-key.pem ec2-user@your-ec2-public-ip
```

### 2. Install Dependencies

```bash
# Update system
sudo dnf update -y

# Install Go 1.23
sudo dnf install -y golang

# Verify Go installation
go version

# Install git if not present
sudo dnf install -y git

# Install screen or tmux for persistent sessions
sudo dnf install -y screen
```

### 3. Clone and Setup Your Project

```bash
# Clone your repository (adjust URL as needed)
cd ~
git clone <your-repo-url> zk-tachyon-solver
cd zk-tachyon-solver/tachyon-prod
```

### 4. Configure Environment Variables

Create a `.env` file with your configuration:

```bash
nano .env
```

Add the following (replace with your actual values):

```bash
# Chain Configurations
CHAIN_COUNT=3

# Chain 1 - Horizen (example)
CHAIN_1_ID=845320009
CHAIN_1_NAME=horizen
CHAIN_1_RPC=https://rpc-testnet.horizenlabs.io/ethv1
CHAIN_1_BRIDGE=0xYourBridgeAddress
CHAIN_1_EXPLORER=https://explorer-testnet.horizen.io

# Chain 2 - Base (example)
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

# Wallet Configuration
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
```

Press `Ctrl+X`, then `Y`, then `Enter` to save.

### 5. Build the Services

```bash
make build
```

This creates two binaries:
- `bin/relayer`
- `bin/solver`

### 6. Start Services with Screen

Using screen allows services to run even after you disconnect from SSH.

#### Start Relayer

```bash
screen -S relayer
cd ~/zk-tachyon-solver/tachyon-prod
./bin/relayer
```

Press `Ctrl+A` then `D` to detach from the screen session.

#### Start Solver

```bash
screen -S solver
cd ~/zk-tachyon-solver/tachyon-prod
./bin/solver
```

Press `Ctrl+A` then `D` to detach from the screen session.

### 7. Verify Services are Running

```bash
# Check relayer logs
screen -r relayer
# Press Ctrl+A then D to detach

# Check solver logs
screen -r solver
# Press Ctrl+A then D to detach

# Check if services are listening on ports
sudo netstat -tlnp | grep -E ':(3000|3001)'

# Test relayer endpoint
curl http://localhost:3000/health

# Test solver endpoint
curl http://localhost:3001/status
```

## Using Systemd (Production Recommended)

For production deployments, use systemd to manage services:

### Create Systemd Service Files

#### Relayer Service

```bash
sudo nano /etc/systemd/system/tachyon-relayer.service
```

Add:

```ini
[Unit]
Description=Tachyon Relayer Service
After=network.target

[Service]
Type=simple
User=ec2-user
WorkingDirectory=/home/ec2-user/zk-tachyon-solver/tachyon-prod
Environment="PATH=/usr/local/go/bin:/usr/bin:/bin"
ExecStart=/home/ec2-user/zk-tachyon-solver/tachyon-prod/bin/relayer
Restart=always
RestartSec=10
StandardOutput=append:/home/ec2-user/zk-tachyon-solver/tachyon-prod/relayer.log
StandardError=append:/home/ec2-user/zk-tachyon-solver/tachyon-prod/relayer.log

[Install]
WantedBy=multi-user.target
```

#### Solver Service

```bash
sudo nano /etc/systemd/system/tachyon-solver.service
```

Add:

```ini
[Unit]
Description=Tachyon Solver Service
After=network.target tachyon-relayer.service

[Service]
Type=simple
User=ec2-user
WorkingDirectory=/home/ec2-user/zk-tachyon-solver/tachyon-prod
Environment="PATH=/usr/local/go/bin:/usr/bin:/bin"
ExecStart=/home/ec2-user/zk-tachyon-solver/tachyon-prod/bin/solver
Restart=always
RestartSec=10
StandardOutput=append:/home/ec2-user/zk-tachyon-solver/tachyon-prod/solver.log
StandardError=append:/home/ec2-user/zk-tachyon-solver/tachyon-prod/solver.log

[Install]
WantedBy=multi-user.target
```

### Enable and Start Services

```bash
# Reload systemd
sudo systemctl daemon-reload

# Enable services to start on boot
sudo systemctl enable tachyon-relayer
sudo systemctl enable tachyon-solver

# Start services
sudo systemctl start tachyon-relayer
sudo systemctl start tachyon-solver

# Check status
sudo systemctl status tachyon-relayer
sudo systemctl status tachyon-solver

# View logs
sudo journalctl -u tachyon-relayer -f
sudo journalctl -u tachyon-solver -f
```

## Maintenance Commands

### View Logs

```bash
# Relayer logs
tail -f ~/zk-tachyon-solver/tachyon-prod/relayer.log

# Solver logs
tail -f ~/zk-tachyon-solver/tachyon-prod/solver.log
```

### Restart Services

```bash
sudo systemctl restart tachyon-relayer
sudo systemctl restart tachyon-solver
```

### Stop Services

```bash
sudo systemctl stop tachyon-relayer
sudo systemctl stop tachyon-solver
```

### Update Deployment

```bash
# Stop services
sudo systemctl stop tachyon-solver
sudo systemctl stop tachyon-relayer

# Pull latest code
cd ~/zk-tachyon-solver/tachyon-prod
git pull

# Rebuild
make clean
make build

# Start services
sudo systemctl start tachyon-relayer
sudo systemctl start tachyon-solver
```

## Security Recommendations

1. **Firewall Configuration**
   ```bash
   # If using firewalld
   sudo firewall-cmd --permanent --add-port=3000/tcp
   sudo firewall-cmd --permanent --add-port=3001/tcp
   sudo firewall-cmd --reload
   ```

2. **Protect Environment Variables**
   ```bash
   chmod 600 .env
   ```

3. **Use AWS Secrets Manager** (Optional)
   - Store sensitive keys in AWS Secrets Manager
   - Modify config loading to fetch from Secrets Manager

4. **Enable HTTPS** (Production)
   - Use nginx as reverse proxy with SSL/TLS
   - Obtain certificates from Let's Encrypt

5. **Monitor Disk Space**
   ```bash
   df -h
   ```

6. **Backup Configuration**
   ```bash
   # Backup .env file securely
   cp .env .env.backup
   ```

## Monitoring

### Check Service Health

```bash
# Relayer health check
curl http://localhost:3000/health

# Solver status
curl http://localhost:3001/status

# Check active intents
curl http://localhost:3001/intents

# Check registered routes
curl http://localhost:3000/routes
```

### System Resources

```bash
# CPU and Memory usage
top

# Disk usage
df -h

# Check service resource usage
ps aux | grep -E '(relayer|solver)'
```

## Troubleshooting

### Services Won't Start

1. Check logs:
   ```bash
   sudo journalctl -u tachyon-relayer -n 50
   sudo journalctl -u tachyon-solver -n 50
   ```

2. Verify .env file exists and has correct values
3. Check if ports are available:
   ```bash
   sudo netstat -tlnp | grep -E ':(3000|3001)'
   ```

### Connection Issues

1. Verify security group allows inbound traffic on ports 3000 and 3001
2. Check RPC endpoints are accessible:
   ```bash
   curl -X POST -H "Content-Type: application/json" \
     --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
     https://your-rpc-url
   ```

### Out of Memory

1. Check available memory: `free -h`
2. Consider upgrading to larger instance type
3. Review logs for memory leaks

### Database/State Issues

1. Check if blockchain clients are syncing properly
2. Verify chain configurations in .env
3. Ensure wallet has sufficient balance on all chains

## Cost Optimization

1. **Use Reserved Instances** for long-term deployments
2. **Right-size instance** - Start with t3.medium, monitor usage
3. **Use Spot Instances** for non-critical development/testing
4. **Monitor data transfer costs** - optimize RPC calls
5. **Set up CloudWatch alarms** for cost monitoring

## Support

- Check logs for detailed error messages
- Review [tachyon-prod/README.md](README.md) for project documentation
- Verify blockchain RPC endpoints are working
- Ensure wallet has sufficient funds for gas fees

## Next Steps

1. Set up monitoring and alerting (CloudWatch)
2. Configure automatic backups
3. Set up CI/CD pipeline for deployments
4. Implement log aggregation (CloudWatch Logs)
5. Add reverse proxy with HTTPS (nginx + Let's Encrypt)

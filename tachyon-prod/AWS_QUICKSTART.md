# AWS Quick Start Guide - Console Deployment

This guide provides step-by-step instructions for deploying Tachyon Relayer and Solver on AWS EC2 using the AWS Console.

## Prerequisites

Before you begin, ensure you have:

1. AWS account with EC2 access
2. Ethereum wallet private key
3. RPC endpoints for your target blockchains (Horizen, Base, Sepolia, etc.)
4. Bridge contract addresses deployed on each chain

## Step 1: Launch EC2 Instance via AWS Console

### 1.1 Navigate to EC2

1. Log into [AWS Console](https://console.aws.amazon.com/)
2. Navigate to **EC2** service
3. Click **Launch Instance**

### 1.2 Configure Instance

**Name and Tags:**
- Name: `tachyon-solver-relayer`

**Application and OS Images (AMI):**
- Select **Amazon Linux 2023 AMI**
- Architecture: **64-bit (x86)**

**Instance Type:**
- Recommended: **t3.medium** (2 vCPU, 4 GB RAM)
- Minimum: **t3.small** (2 vCPU, 2 GB RAM)

**Key pair (login):**
- Create a new key pair or select existing
- Download `.pem` file if creating new (you'll need this for SSH)

**Network Settings:**
- VPC: Default or your custom VPC
- Auto-assign public IP: **Enable**

**Configure Security Group:**
Create or select a security group with these rules:

| Type        | Protocol | Port Range | Source          | Description         |
|-------------|----------|------------|-----------------|---------------------|
| SSH         | TCP      | 22         | Your IP/0.0.0.0/0 | SSH access        |
| Custom TCP  | TCP      | 3000       | 0.0.0.0/0       | Relayer service     |
| Custom TCP  | TCP      | 3001       | 0.0.0.0/0       | Solver service      |

**Configure Storage:**
- Size: **20 GB minimum** (30 GB recommended)
- Volume Type: **gp3** (General Purpose SSD)

**Advanced Details:**
- Leave as default

### 1.3 Launch Instance

1. Review configuration
2. Click **Launch Instance**
3. Wait for instance state to become **Running**
4. Note down your **Public IPv4 address**

## Step 2: Connect to Your Instance

### 2.1 Using SSH (Mac/Linux)

```bash
# Set permissions on your key file
chmod 400 your-key.pem

# Connect to instance
ssh -i your-key.pem ec2-user@<YOUR-PUBLIC-IP>
```

### 2.2 Using EC2 Instance Connect (Browser-based)

1. Select your instance in EC2 console
2. Click **Connect**
3. Select **EC2 Instance Connect** tab
4. Click **Connect**

## Step 3: Run Automated Deployment Script

Once connected to your instance, run these commands:

```bash
# Download the deployment script
wget https://raw.githubusercontent.com/TachyonPay/zk-tachyon-solver/master/tachyon-prod/scripts/aws-console-deploy.sh

# Make it executable
chmod +x aws-console-deploy.sh

# Run the script
./aws-console-deploy.sh
```

The script will:
1. Update system packages
2. Install Go 1.23
3. Install required tools (git, screen, etc.)
4. Clone the Tachyon repository
5. Create/edit `.env` configuration file
6. Build the services
7. Set up systemd services
8. Start the relayer and solver

### 3.1 Configure Environment Variables

When prompted, you'll edit the `.env` file. **Important variables to update:**

```bash
# Your Ethereum wallet private key (without 0x prefix)
PRIVATE_KEY=your_actual_private_key_here

# Chain 1 - Horizen
CHAIN_1_BRIDGE=0xYourActualBridgeAddress

# Chain 2 - Base
CHAIN_2_RPC=https://base-sepolia.g.alchemy.com/v2/YOUR-ALCHEMY-KEY
CHAIN_2_BRIDGE=0xYourActualBridgeAddress

# Chain 3 - Sepolia
CHAIN_3_RPC=https://eth-sepolia.g.alchemy.com/v2/YOUR-ALCHEMY-KEY
CHAIN_3_BRIDGE=0xYourActualBridgeAddress
```

Use `nano` editor commands:
- Arrow keys to navigate
- Type to edit
- `Ctrl + X` to exit
- `Y` to save
- `Enter` to confirm

## Step 4: Verify Deployment

### 4.1 Check Service Status

```bash
# Check if services are running
sudo systemctl status tachyon-relayer
sudo systemctl status tachyon-solver

# Both should show "active (running)" in green
```

### 4.2 Test Endpoints

```bash
# Test relayer
curl http://localhost:3000/health

# Test solver
curl http://localhost:3001/status

# Check active intents
curl http://localhost:3001/intents

# Check routes
curl http://localhost:3000/routes
```

### 4.3 View Logs

```bash
# Watch relayer logs in real-time
sudo journalctl -u tachyon-relayer -f

# Watch solver logs in real-time (open new terminal)
sudo journalctl -u tachyon-solver -f

# View last 50 lines of logs
sudo journalctl -u tachyon-relayer -n 50
sudo journalctl -u tachyon-solver -n 50

# Or view log files directly
tail -f ~/zk-tachyon-solver/tachyon-prod/relayer.log
tail -f ~/zk-tachyon-solver/tachyon-prod/solver.log
```

## Step 5: Access Services Externally

Your services are now accessible via:

- **Relayer**: `http://<YOUR-PUBLIC-IP>:3000`
- **Solver**: `http://<YOUR-PUBLIC-IP>:3001`

Test from your local machine:

```bash
# Replace <YOUR-PUBLIC-IP> with your actual EC2 public IP
curl http://<YOUR-PUBLIC-IP>:3000/health
curl http://<YOUR-PUBLIC-IP>:3001/status
```

## Common Management Commands

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

### Start Services

```bash
sudo systemctl start tachyon-relayer
sudo systemctl start tachyon-solver
```

### View Service Status

```bash
sudo systemctl status tachyon-relayer
sudo systemctl status tachyon-solver
```

### Update to Latest Code

```bash
# Stop services
sudo systemctl stop tachyon-solver tachyon-relayer

# Pull latest code
cd ~/zk-tachyon-solver
git pull

# Rebuild
cd tachyon-prod
make clean
make build

# Restart services
sudo systemctl start tachyon-relayer tachyon-solver
```

## Troubleshooting

### Services Won't Start

**Check logs for errors:**
```bash
sudo journalctl -u tachyon-relayer -n 100
sudo journalctl -u tachyon-solver -n 100
```

**Common issues:**
1. **Invalid private key**: Check `PRIVATE_KEY` in `.env`
2. **RPC connection failed**: Verify RPC URLs are correct and accessible
3. **Port already in use**: Check if another process is using ports 3000/3001

### Can't Connect to Services from Outside

**Check security group:**
1. Go to EC2 Console
2. Select your instance
3. Click **Security** tab
4. Verify inbound rules for ports 3000 and 3001

**Check if services are listening:**
```bash
sudo netstat -tlnp | grep -E ':(3000|3001)'
```

### Out of Memory

**Check memory usage:**
```bash
free -h
top
```

**If needed, upgrade instance:**
1. Stop instance in EC2 console
2. Actions → Instance Settings → Change Instance Type
3. Select larger type (e.g., t3.large)
4. Start instance

### Blockchain Connection Issues

**Test RPC endpoints:**
```bash
# Test Horizen
curl -X POST https://rpc-testnet.horizenlabs.io/ethv1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Test Base Sepolia (replace with your Alchemy key)
curl -X POST https://base-sepolia.g.alchemy.com/v2/YOUR-KEY \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Security Best Practices

1. **Restrict SSH access**: Update security group to allow SSH only from your IP
2. **Protect your private key**: Never share or commit your `.env` file
3. **Use HTTPS**: Set up nginx with SSL/TLS for production
4. **Regular backups**: Backup your `.env` configuration
5. **Monitor logs**: Set up CloudWatch for log monitoring
6. **Update regularly**: Keep system and dependencies updated

## Cost Estimation

**Approximate monthly costs (us-east-1):**

- **t3.small**: ~$15/month (minimum)
- **t3.medium**: ~$30/month (recommended)
- **t3.large**: ~$60/month (high load)
- **Data transfer**: Variable based on usage
- **EBS storage (20 GB)**: ~$2/month

Use AWS Cost Explorer to monitor actual costs.

## Next Steps

1. ✅ Services deployed and running
2. ✅ Test basic functionality
3. Monitor logs for any issues
4. Fund your solver wallet on all chains
5. Register solver routes with relayer
6. Monitor performance and optimize
7. Set up monitoring and alerting
8. Consider setting up HTTPS for production

## Support

For issues or questions:
- Check logs: `sudo journalctl -u tachyon-relayer -f`
- Review [AWS_DEPLOYMENT.md](AWS_DEPLOYMENT.md) for detailed documentation
- Check GitHub repository: https://github.com/TachyonPay/zk-tachyon-solver

## Manual Alternative (Without Script)

If you prefer manual setup or the script fails, follow the detailed steps in [AWS_DEPLOYMENT.md](AWS_DEPLOYMENT.md).

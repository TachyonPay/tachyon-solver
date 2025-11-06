# AWS Deployment Scripts

This directory contains scripts for deploying and managing Tachyon Relayer and Solver services on AWS EC2.

## Available Scripts

### 1. `aws-console-deploy.sh` (Recommended for Manual AWS Console Deployment)

**Purpose**: Complete deployment script for EC2 instances launched via AWS Console.

**Features**:
- Interactive setup with prompts
- Automatic dependency installation
- Repository cloning from GitHub
- Environment configuration helper
- Service build and deployment
- Systemd service setup

**Usage**:
```bash
# SSH into your EC2 instance
ssh -i your-key.pem ec2-user@your-ec2-ip

# Download and run the script
wget https://raw.githubusercontent.com/TachyonPay/zk-tachyon-solver/master/tachyon-prod/scripts/aws-console-deploy.sh
chmod +x aws-console-deploy.sh
./aws-console-deploy.sh
```

**What it does**:
1. Updates Amazon Linux 2023 system
2. Installs Go 1.23
3. Installs required tools (git, screen, net-tools, curl)
4. Clones the Tachyon repository from GitHub
5. Creates/edits `.env` configuration file
6. Downloads dependencies and builds services
7. Sets up systemd services for auto-restart
8. Starts the relayer and solver services

### 2. `aws-setup.sh` (Automated Non-Interactive)

**Purpose**: Fully automated deployment for scripting/CI-CD pipelines.

**Features**:
- Non-interactive (requires pre-configured repository)
- Automated service setup
- Suitable for infrastructure-as-code deployments

**Usage**:
```bash
# Clone repository first
git clone https://github.com/TachyonPay/zk-tachyon-solver.git
cd zk-tachyon-solver/tachyon-prod

# Create and configure .env file
nano .env

# Run setup script
./scripts/aws-setup.sh
```

**Prerequisites**:
- Repository already cloned
- `.env` file configured
- Running on Amazon Linux 2023

### 3. `aws-update.sh` (Service Update Script)

**Purpose**: Update running services to latest code version.

**Features**:
- Safe update process (stops services before updating)
- Git pull from master branch
- Rebuild services
- Automatic service restart
- Status verification

**Usage**:
```bash
cd ~/zk-tachyon-solver/tachyon-prod
./scripts/aws-update.sh
```

**What it does**:
1. Shows current version
2. Stops running services
3. Pulls latest code from GitHub
4. Rebuilds services
5. Restarts services
6. Verifies services are running

## Quick Start Guide

### For New AWS EC2 Deployment

If you're deploying for the first time:

1. **Launch EC2 Instance** (via AWS Console)
   - AMI: Amazon Linux 2023
   - Instance Type: t3.medium (recommended)
   - Security Group: Allow ports 22, 3000, 3001

2. **Connect via SSH**
   ```bash
   ssh -i your-key.pem ec2-user@<your-ec2-ip>
   ```

3. **Run Deployment Script**
   ```bash
   wget https://raw.githubusercontent.com/TachyonPay/zk-tachyon-solver/master/tachyon-prod/scripts/aws-console-deploy.sh
   chmod +x aws-console-deploy.sh
   ./aws-console-deploy.sh
   ```

4. **Follow Prompts**
   - The script will guide you through configuration
   - You'll edit the `.env` file with your settings
   - Services will be built and started automatically

### For Updating Existing Deployment

If services are already running:

```bash
cd ~/zk-tachyon-solver/tachyon-prod
./scripts/aws-update.sh
```

## Script Comparison

| Feature | aws-console-deploy.sh | aws-setup.sh | aws-update.sh |
|---------|----------------------|--------------|---------------|
| Interactive | Yes | No | Yes |
| First-time setup | ✅ | ✅ | ❌ |
| Updates existing | ✅ | ❌ | ✅ |
| Requires .env | Creates it | Pre-configured | Uses existing |
| Clones repo | ✅ | ❌ | ❌ |
| Systemd setup | ✅ | ✅ | Uses existing |
| Best for | Manual deployment | CI/CD | Updates |

## Environment Configuration

All scripts require a `.env` file in the `tachyon-prod` directory. Key variables:

```bash
# Required
PRIVATE_KEY=your_wallet_private_key_without_0x

# Chain configurations
CHAIN_COUNT=3
CHAIN_1_ID=845320009
CHAIN_1_RPC=https://rpc-testnet.horizenlabs.io/ethv1
CHAIN_1_BRIDGE=0xYourBridgeAddress
# ... more chains

# Service ports
RELAYER_PORT=3000
SOLVER_PORT=3001
```

See [AWS_QUICKSTART.md](../AWS_QUICKSTART.md) for complete configuration guide.

## Systemd Services

All scripts set up systemd services for automatic restart and management:

```bash
# Service management
sudo systemctl start tachyon-relayer
sudo systemctl start tachyon-solver
sudo systemctl stop tachyon-relayer
sudo systemctl stop tachyon-solver
sudo systemctl restart tachyon-relayer
sudo systemctl restart tachyon-solver
sudo systemctl status tachyon-relayer
sudo systemctl status tachyon-solver

# View logs
sudo journalctl -u tachyon-relayer -f
sudo journalctl -u tachyon-solver -f
```

## Log Files

Services write logs to:
- Relayer: `~/zk-tachyon-solver/tachyon-prod/relayer.log`
- Solver: `~/zk-tachyon-solver/tachyon-prod/solver.log`

View logs:
```bash
tail -f ~/zk-tachyon-solver/tachyon-prod/relayer.log
tail -f ~/zk-tachyon-solver/tachyon-prod/solver.log
```

## Troubleshooting

### Script fails to download

**Issue**: Cannot download script from GitHub

**Solution**:
```bash
# Manually clone repository
git clone https://github.com/TachyonPay/zk-tachyon-solver.git
cd zk-tachyon-solver/tachyon-prod
./scripts/aws-console-deploy.sh
```

### Build fails

**Issue**: Go build fails

**Solution**:
```bash
# Verify Go installation
go version

# Clean and rebuild
cd ~/zk-tachyon-solver/tachyon-prod
make clean
go mod download
go mod tidy
make build
```

### Services won't start

**Issue**: Systemd service fails to start

**Solution**:
```bash
# Check logs
sudo journalctl -u tachyon-relayer -n 50
sudo journalctl -u tachyon-solver -n 50

# Verify .env configuration
cat ~/zk-tachyon-solver/tachyon-prod/.env | grep PRIVATE_KEY

# Check if ports are available
sudo netstat -tlnp | grep -E ':(3000|3001)'
```

### Permission denied

**Issue**: Script not executable

**Solution**:
```bash
chmod +x aws-console-deploy.sh
chmod +x aws-update.sh
```

## Security Notes

1. **Never commit `.env`** - Contains sensitive keys
2. **Protect scripts** - Ensure only authorized users can run them
3. **Use SSH keys** - Don't use password authentication
4. **Restrict security groups** - Only allow necessary ports
5. **Backup `.env`** - Keep secure backup of configuration

## Additional Resources

- [AWS_DEPLOYMENT.md](../AWS_DEPLOYMENT.md) - Detailed deployment guide
- [AWS_QUICKSTART.md](../AWS_QUICKSTART.md) - Quick start guide for AWS Console
- [README.md](../README.md) - Project documentation

## Support

For issues with scripts:
1. Check logs: `sudo journalctl -u tachyon-relayer -f`
2. Verify environment: `cat .env`
3. Test services: `curl http://localhost:3000/health`
4. Review documentation in parent directory

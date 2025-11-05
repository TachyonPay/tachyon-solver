# Quick Start Guide

This guide will help you get the Tachyon-Prod services up and running quickly.

## Prerequisites Check

Before starting, ensure you have:

- [ ] Go 1.21 or higher installed (`go version`)
- [ ] Private key with testnet funds
- [ ] Access to Horizen Testnet and Base Sepolia RPCs

## Step-by-Step Setup

### 1. Navigate to Project Directory

```bash
cd tachyon-prod
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Configure Environment

```bash
# Copy the example environment file
cp .env.example .env

# Edit with your values (use nano, vim, or any editor)
nano .env
```

**Required changes in `.env`:**
- Update `PRIVATE_KEY` with your wallet's private key
- Verify `BRIDGE_CONTRACT_HORIZEN_LATEST` address
- Verify `BRIDGE_CONTRACT_BASE_LATEST` address

### 4. Build Services

```bash
# Build both services
make build

# Or build individually
make build-solver
make build-relayer
```

### 5. Start Services

**Terminal 1 - Start Relayer:**
```bash
./bin/relayer
```

Expected output:
```
INFO[2025-11-05T10:30:00Z] Starting Tachyon Relayer Service...
INFO[2025-11-05T10:30:00Z] Configuration loaded networks=2 port=3000
INFO[2025-11-05T10:30:00Z] Relayer blockchain client initialized network=horizen
INFO[2025-11-05T10:30:00Z] Relayer blockchain client initialized network=base
INFO[2025-11-05T10:30:00Z] Relayer initialized successfully
INFO[2025-11-05T10:30:00Z] Starting HTTP server address=:3000
```

**Terminal 2 - Start Solver:**
```bash
./bin/solver
```

Expected output:
```
INFO[2025-11-05T10:30:00Z] Starting Tachyon Solver Service...
INFO[2025-11-05T10:30:00Z] Configuration loaded networks=2 port=3001
INFO[2025-11-05T10:30:00Z] Blockchain client initialized network=horizen
INFO[2025-11-05T10:30:00Z] Blockchain client initialized network=base
INFO[2025-11-05T10:30:00Z] Solver initialized successfully
INFO[2025-11-05T10:30:00Z] Starting event polling network=horizen
INFO[2025-11-05T10:30:00Z] Starting event polling network=base
INFO[2025-11-05T10:30:00Z] Starting HTTP server address=:3001
```

## Testing the Services

### Test Relayer Health

```bash
curl http://localhost:3000/health
```

Expected response:
```json
{
  "status": "healthy",
  "service": "bridge-relayer",
  "timestamp": "2025-11-05T10:30:00Z",
  "networks": ["horizen", "base"],
  "chainInfo": {
    "horizen": "Chain Horizen Testnet (ID: 845320009)",
    "base": "Chain Base Sepolia (ID: 84532)"
  }
}
```

### Test Solver Health

```bash
curl http://localhost:3001/health
```

Expected response:
```json
{
  "status": "healthy",
  "service": "bridge-solver",
  "timestamp": "2025-11-05T10:30:00Z",
  "activeIntents": 0
}
```

### Check Solver Status

```bash
curl http://localhost:3001/status
```

This will show:
- Running status
- Wallet address
- Token balances
- Active intents

## Alternative: Run Without Building

You can also run the services directly without building:

**Terminal 1:**
```bash
go run ./cmd/relayer
```

**Terminal 2:**
```bash
go run ./cmd/solver
```

## Troubleshooting

### "Failed to load configuration"
- Check that `.env` file exists
- Verify all required environment variables are set
- Ensure private key has `0x` prefix

### "Failed to connect to Ethereum client"
- Verify RPC URLs are correct and accessible
- Check network connectivity
- Try alternative RPC endpoints

### "Failed to parse private key"
- Ensure private key is in hex format with `0x` prefix
- Verify the key is valid (64 hex characters after `0x`)

### Port Already in Use
- Check if services are already running: `lsof -i :3000` or `lsof -i :3001`
- Change ports in `.env` if needed:
  ```bash
  SOLVER_PORT=3002
  RELAYER_PORT=3001
  ```

## Stopping Services

Press `Ctrl+C` in each terminal to gracefully shutdown the services.

Expected shutdown output:
```
INFO[2025-11-05T10:35:00Z] Shutting down solver...
INFO[2025-11-05T10:35:00Z] Stopping event polling network=horizen
INFO[2025-11-05T10:35:00Z] Stopping event polling network=base
INFO[2025-11-05T10:35:00Z] Solver shutdown complete
```

## Next Steps

Once services are running:

1. **Monitor Logs**: Watch for intent detection and processing
2. **Check Active Intents**: `curl http://localhost:3001/active-intents`
3. **Create Test Intent**: Use the original scripts or create one manually
4. **Monitor Settlement**: Watch relayer logs for settlement operations

## Development Mode

For development with more verbose logging, edit the main files and change:

```go
log.SetLevel(log.DebugLevel)
```

Then rebuild and run.

## Production Deployment

For production deployment:

1. Use secure key management (not `.env` files)
2. Set up proper monitoring and alerting
3. Configure log aggregation
4. Use process managers (systemd, supervisord)
5. Set up load balancing if needed
6. Implement proper backup and recovery

## Getting Help

If you encounter issues:

1. Check logs for error messages
2. Verify configuration in `.env`
3. Test network connectivity to RPCs
4. Ensure sufficient testnet funds
5. Review the main README.md for detailed information

## Useful Commands

```bash
# Build
make build

# Clean and rebuild
make clean build

# Run tests
make test

# Format code
make fmt

# Check dependencies
go mod verify

# Update dependencies
go get -u ./...
go mod tidy
```

Happy solving! 🚀

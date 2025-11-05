# Tachyon-Prod Documentation Index

Welcome to the Tachyon-Prod Golang implementation! This index will help you navigate the documentation.

## 🚀 Getting Started

**New to this project?** Start here:

1. **[QUICKSTART.md](QUICKSTART.md)** ⭐
   - Step-by-step setup guide
   - Prerequisites check
   - Build and run instructions
   - Testing the services
   - Troubleshooting

## 📖 Main Documentation

2. **[README.md](README.md)** 📚
   - Complete project overview
   - Architecture description
   - API documentation
   - Configuration options
   - Security considerations
   - Deployment guide

## 🔄 Migration

3. **[MIGRATION_GUIDE.md](MIGRATION_GUIDE.md)** 🔄
   - TypeScript → Golang differences
   - Code translation examples
   - Data type mappings
   - Verification changes
   - Migration checklist
   - API compatibility

## 🏗️ Architecture

4. **[PROJECT_STRUCTURE.md](PROJECT_STRUCTURE.md)** 🏗️
   - Detailed directory structure
   - Package descriptions
   - Data flow diagrams
   - Concurrency model
   - Error handling
   - Testing strategy

## 🎯 Quick Reference

### File Organization

```
tachyon-prod/
├── cmd/                    # Application entry points
│   ├── solver/            # Solver service
│   └── relayer/           # Relayer service
│
├── pkg/                    # Core packages
│   ├── api/               # HTTP handlers
│   ├── blockchain/        # Ethereum client
│   ├── config/            # Configuration
│   ├── relayer/           # Relayer logic
│   ├── solver/            # Solver logic
│   └── types/             # Shared types
│
├── contracts/              # Smart contracts
├── .env.example           # Configuration template
├── Makefile              # Build automation
└── [documentation files]
```

### Essential Commands

```bash
# Setup
cp .env.example .env      # Configure environment
make build                # Build both services

# Running
./bin/relayer            # Start relayer (port 3000)
./bin/solver             # Start solver (port 3001)

# Development
make run-relayer         # Run without building
make run-solver          # Run without building
make test                # Run tests
make clean               # Clean build artifacts
```

### API Endpoints

**Solver (Port 3001)**
- `GET /health` - Health check
- `GET /status` - Detailed status
- `GET /active-intents` - List active intents
- `POST /start` - Start solver
- `POST /stop` - Stop solver

**Relayer (Port 3000)**
- `GET /health` - Health check
- `POST /store-recipients` - Store recipients
- `GET /get-recipients/:id` - Get recipients
- `POST /settle` - Settle intent

## 📋 Document Summary

| Document | Purpose | When to Read |
|----------|---------|--------------|
| **QUICKSTART.md** | Get up and running quickly | First time setup |
| **README.md** | Comprehensive reference | Understanding the system |
| **MIGRATION_GUIDE.md** | Migrating from TypeScript | Coming from TS version |
| **PROJECT_STRUCTURE.md** | Architecture deep-dive | Development & extending |
| **INDEX.md** | This file | Finding documentation |

## 🎓 Learning Path

### For First-Time Users
1. Read [QUICKSTART.md](QUICKSTART.md)
2. Follow setup instructions
3. Test the health endpoints
4. Refer to [README.md](README.md) for details

### For Developers
1. Read [PROJECT_STRUCTURE.md](PROJECT_STRUCTURE.md)
2. Understand the architecture
3. Review code in `pkg/` directory
4. Check [README.md](README.md) for API details

### For Migrators
1. Read [MIGRATION_GUIDE.md](MIGRATION_GUIDE.md)
2. Compare TypeScript vs Golang code
3. Follow migration checklist
4. Test both versions side-by-side

## 🔍 Finding Information

### Configuration
- Environment variables: **README.md** → Configuration section
- Default values: **pkg/config/config.go**
- Example: **.env.example**

### API Usage
- Endpoint list: **README.md** → API Documentation
- Request/Response formats: **pkg/types/types.go**
- Handler implementation: **pkg/api/**

### Architecture
- System overview: **README.md** → Architecture
- Detailed structure: **PROJECT_STRUCTURE.md**
- Data flow: **PROJECT_STRUCTURE.md** → Data Flow

### Deployment
- Local setup: **QUICKSTART.md**
- Production: **README.md** → Deployment
- Troubleshooting: **QUICKSTART.md** → Troubleshooting

## 💡 Quick Tips

### Building
```bash
make build              # Standard build
make build-optimized    # Smaller binaries
```

### Running
```bash
# With binaries
./bin/relayer
./bin/solver

# Or directly with Go
go run ./cmd/relayer
go run ./cmd/solver
```

### Testing
```bash
# Health checks
curl http://localhost:3000/health
curl http://localhost:3001/health

# Status
curl http://localhost:3001/status
```

### Debugging
```bash
# Enable debug logging in main.go:
log.SetLevel(log.DebugLevel)
```

## 🆘 Getting Help

1. **Check the docs** - Most questions are answered here
2. **Review logs** - Enable debug logging for details
3. **Check configuration** - Verify .env file
4. **Test connectivity** - Ensure RPC access works
5. **Compare with TypeScript** - Check original behavior

## 📦 Additional Resources

### In This Directory
- **go.mod** - Go module dependencies
- **Makefile** - Build commands and targets
- **.gitignore** - Git ignore patterns
- **.env.example** - Configuration template

### Parent Directory
- **CONVERSION_SUMMARY.md** - High-level conversion overview
- **Original TypeScript** - solver/ and relayer/ directories

## ✅ Checklist

Before starting development:
- [ ] Read QUICKSTART.md
- [ ] Configure .env file
- [ ] Build successfully
- [ ] Test health endpoints
- [ ] Understand architecture

## 🎯 Next Steps

Choose your path:

- **Quick Start**: Go to [QUICKSTART.md](QUICKSTART.md)
- **Deep Dive**: Read [README.md](README.md)
- **Migration**: See [MIGRATION_GUIDE.md](MIGRATION_GUIDE.md)
- **Architecture**: Study [PROJECT_STRUCTURE.md](PROJECT_STRUCTURE.md)

---

**Happy coding! 🚀**

Need help? All documentation is self-contained in this directory.

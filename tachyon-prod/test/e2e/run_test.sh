#!/bin/bash

# E2E Test Runner Script
# This script runs the complete end-to-end test for the Tachyon bridge

set -e

echo "🚀 Tachyon-Prod E2E Test Runner"
echo "================================"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if .env.test exists
if [ ! -f ".env.test" ]; then
    echo -e "${RED}❌ .env.test file not found${NC}"
    echo "Please copy .env.test.example to .env.test and configure it"
    echo ""
    echo "  cp .env.test.example .env.test"
    echo "  nano .env.test  # Edit with your values"
    exit 1
fi

echo -e "${GREEN}✅ Found .env.test configuration${NC}"

# Check if services are running
echo ""
echo "🏥 Checking service health..."

# Check Relayer
if curl -s http://localhost:3000/health > /dev/null 2>&1; then
    echo -e "${GREEN}✅ Relayer service is running${NC}"
else
    echo -e "${RED}❌ Relayer service is not responding${NC}"
    echo "Please start the relayer service:"
    echo "  cd ../../"
    echo "  ./bin/relayer"
    exit 1
fi

# Check Solver
if curl -s http://localhost:3001/health > /dev/null 2>&1; then
    echo -e "${GREEN}✅ Solver service is running${NC}"
else
    echo -e "${YELLOW}⚠️  Solver service is not responding${NC}"
    echo "Note: Tests can run without solver, but may not be complete"
fi

echo ""
echo "📦 Building test..."
echo "-------------------"

# Build the test
go build -o e2e_test complete_flow.go

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Test built successfully${NC}"
else
    echo -e "${RED}❌ Test build failed${NC}"
    exit 1
fi

echo ""
echo "🧪 Running E2E Test..."
echo "====================="
echo ""

# Run the test
./e2e_test

TEST_RESULT=$?

echo ""
echo "================================"

if [ $TEST_RESULT -eq 0 ]; then
    echo -e "${GREEN}✅ E2E Test PASSED${NC}"
    echo ""
    echo "🎉 All tests completed successfully!"
else
    echo -e "${RED}❌ E2E Test FAILED${NC}"
    echo ""
    echo "Please check the output above for error details"
    exit 1
fi

# Cleanup
rm -f e2e_test

echo ""
echo "Test run completed at $(date)"

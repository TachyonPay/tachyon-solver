#!/bin/bash

###############################################################################
# Setup All Tachyon Subdomains
#
# This script configures HTTPS for all Tachyon subdomains:
# - relayer.tachyon.pe (port 3000)
# - solver.tachyon.pe (port 3001)
#
# Usage: sudo ./setup-all-subdomains.sh <email>
# Example: sudo ./setup-all-subdomains.sh admin@tachyon.pe
###############################################################################

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Please run as root (use sudo)${NC}"
    exit 1
fi

if [ $# -lt 1 ]; then
    echo -e "${RED}Usage: $0 <email>${NC}"
    echo "Example: $0 admin@tachyon.pe"
    exit 1
fi

EMAIL=$1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Tachyon Multi-Subdomain HTTPS Setup${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "This will setup HTTPS for:"
echo "  - relayer.tachyon.pe → localhost:3000"
echo "  - solver.tachyon.pe  → localhost:3001"
echo ""
echo -e "${YELLOW}Current server IP: $(curl -s ifconfig.me)${NC}"
echo ""
echo -e "${YELLOW}IMPORTANT: Before proceeding, ensure you've added these A records in GoDaddy:${NC}"
echo "  relayer.tachyon.pe → $(curl -s ifconfig.me)"
echo "  solver.tachyon.pe  → $(curl -s ifconfig.me)"
echo ""
read -p "Have you configured the DNS records? (y/n) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${YELLOW}Please add the DNS records first, then run this script again${NC}"
    exit 1
fi
echo ""

# Setup relayer subdomain
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}[1/2] Setting up relayer.tachyon.pe${NC}"
echo -e "${GREEN}========================================${NC}"
if [ -f "$SCRIPT_DIR/https-setup.sh" ]; then
    "$SCRIPT_DIR/https-setup.sh" relayer.tachyon.pe "$EMAIL" 3000
else
    echo -e "${RED}Error: https-setup.sh not found${NC}"
    exit 1
fi
echo ""

# Setup solver subdomain
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}[2/2] Setting up solver.tachyon.pe${NC}"
echo -e "${GREEN}========================================${NC}"
if [ -f "$SCRIPT_DIR/https-setup.sh" ]; then
    "$SCRIPT_DIR/https-setup.sh" solver.tachyon.pe "$EMAIL" 3001
else
    echo -e "${RED}Error: https-setup.sh not found${NC}"
    exit 1
fi
echo ""

# Final summary
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}All Subdomains Configured!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "${GREEN}Your services are now accessible at:${NC}"
echo -e "${GREEN}  https://relayer.tachyon.pe (port 3000)${NC}"
echo -e "${GREEN}  https://solver.tachyon.pe (port 3001)${NC}"
echo ""
echo -e "${YELLOW}Testing:${NC}"
echo "  curl https://relayer.tachyon.pe"
echo "  curl https://solver.tachyon.pe"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Ensure your applications are running on ports 3000 and 3001"
echo "2. Test both endpoints"
echo "3. Monitor logs: sudo tail -f /var/log/nginx/access.log"
echo ""

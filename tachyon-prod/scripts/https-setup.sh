#!/bin/bash

###############################################################################
# HTTPS Setup Script for Tachyon Relayer
#
# This script automates the setup of:
# - Nginx reverse proxy
# - Let's Encrypt SSL certificate
# - Auto-renewal configuration
#
# Usage: sudo ./https-setup.sh <domain> <email> <app-port>
# Example: sudo ./https-setup.sh relayer.tachyon.pe admin@tachyon.pe 3000
###############################################################################

set -e  # Exit on error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Please run as root (use sudo)${NC}"
    exit 1
fi

# Check arguments
if [ $# -lt 3 ]; then
    echo -e "${RED}Usage: $0 <domain> <email> <app-port>${NC}"
    echo "Example: $0 relayer.tachyon.pe admin@tachyon.pe 8080"
    exit 1
fi

DOMAIN=$1
EMAIL=$2
APP_PORT=$3

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}HTTPS Setup for $DOMAIN${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Verify DNS resolution
echo -e "${YELLOW}[1/7] Verifying DNS resolution...${NC}"
if ! nslookup "$DOMAIN" > /dev/null 2>&1; then
    echo -e "${RED}DNS resolution failed for $DOMAIN${NC}"
    echo -e "${YELLOW}Please ensure you've added an A record in GoDaddy pointing to this server's IP${NC}"
    echo -e "${YELLOW}Current server IP: $(curl -s ifconfig.me)${NC}"
    echo ""
    read -p "DNS not resolved yet. Continue anyway? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
else
    echo -e "${GREEN}DNS resolved successfully!${NC}"
fi
echo ""

# Update system
echo -e "${YELLOW}[2/7] Updating system packages...${NC}"
apt update -qq
echo -e "${GREEN}System updated${NC}"
echo ""

# Install Nginx
echo -e "${YELLOW}[3/7] Installing Nginx...${NC}"
if ! command -v nginx &> /dev/null; then
    apt install -y nginx
    systemctl start nginx
    systemctl enable nginx
    echo -e "${GREEN}Nginx installed and started${NC}"
else
    echo -e "${GREEN}Nginx already installed${NC}"
fi
echo ""

# Install Certbot
echo -e "${YELLOW}[4/7] Installing Certbot...${NC}"
if ! command -v certbot &> /dev/null; then
    apt install -y certbot python3-certbot-nginx
    echo -e "${GREEN}Certbot installed${NC}"
else
    echo -e "${GREEN}Certbot already installed${NC}"
fi
echo ""

# Configure Nginx
echo -e "${YELLOW}[5/7] Configuring Nginx...${NC}"
NGINX_CONF="/etc/nginx/sites-available/$DOMAIN"

cat > "$NGINX_CONF" <<EOF
server {
    listen 80;
    server_name $DOMAIN;

    # Increase timeouts for long-running requests
    proxy_connect_timeout 600;
    proxy_send_timeout 600;
    proxy_read_timeout 600;
    send_timeout 600;

    # Increase buffer sizes
    client_max_body_size 50M;
    client_body_buffer_size 128k;

    location / {
        proxy_pass http://localhost:$APP_PORT;
        proxy_http_version 1.1;

        # WebSocket support
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection 'upgrade';

        # Standard proxy headers
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # Disable caching for dynamic content
        proxy_cache_bypass \$http_upgrade;
        proxy_no_cache \$http_upgrade;

        # Add CORS headers if needed
        add_header Access-Control-Allow-Origin * always;
        add_header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, OPTIONS" always;
        add_header Access-Control-Allow-Headers "Authorization, Content-Type" always;

        if (\$request_method = 'OPTIONS') {
            return 204;
        }
    }

    # Health check endpoint
    location /health {
        access_log off;
        return 200 "healthy\n";
        add_header Content-Type text/plain;
    }
}
EOF

# Enable site
ln -sf "$NGINX_CONF" "/etc/nginx/sites-enabled/$DOMAIN"

# Test Nginx configuration
if nginx -t 2>&1 | grep -q "successful"; then
    systemctl reload nginx
    echo -e "${GREEN}Nginx configured successfully${NC}"
else
    echo -e "${RED}Nginx configuration test failed${NC}"
    nginx -t
    exit 1
fi
echo ""

# Check if application is running
echo -e "${YELLOW}[6/7] Checking if application is running on port $APP_PORT...${NC}"
if netstat -tlnp | grep -q ":$APP_PORT"; then
    echo -e "${GREEN}Application is running on port $APP_PORT${NC}"
else
    echo -e "${YELLOW}Warning: No application detected on port $APP_PORT${NC}"
    echo -e "${YELLOW}Make sure to start your application before testing${NC}"
fi
echo ""

# Obtain SSL certificate
echo -e "${YELLOW}[7/7] Obtaining SSL certificate from Let's Encrypt...${NC}"
echo -e "${YELLOW}This may take a minute...${NC}"

# Check if certificate already exists
if [ -d "/etc/letsencrypt/live/$DOMAIN" ]; then
    echo -e "${YELLOW}Certificate already exists for $DOMAIN${NC}"
    read -p "Renew certificate? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --email "$EMAIL" --force-renewal
    fi
else
    certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --email "$EMAIL" --redirect
fi

echo -e "${GREEN}SSL certificate obtained and installed${NC}"
echo ""

# Test auto-renewal
echo -e "${YELLOW}Testing certificate auto-renewal...${NC}"
certbot renew --dry-run
echo ""

# Setup auto-renewal timer (should be automatic with certbot)
if systemctl list-timers | grep -q certbot; then
    echo -e "${GREEN}Auto-renewal timer is active${NC}"
else
    echo -e "${YELLOW}Setting up auto-renewal timer...${NC}"
    systemctl enable certbot.timer
    systemctl start certbot.timer
fi
echo ""

# Final summary
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Setup Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "${GREEN}Your site is now accessible at:${NC}"
echo -e "${GREEN}  https://$DOMAIN${NC}"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Ensure your application is running on port $APP_PORT"
echo "2. Test your site: curl https://$DOMAIN"
echo "3. Check SSL grade: https://www.ssllabs.com/ssltest/analyze.html?d=$DOMAIN"
echo ""
echo -e "${YELLOW}Useful commands:${NC}"
echo "  - View Nginx logs: sudo tail -f /var/log/nginx/access.log"
echo "  - Test Nginx config: sudo nginx -t"
echo "  - Reload Nginx: sudo systemctl reload nginx"
echo "  - Check certificate: sudo certbot certificates"
echo "  - Renew certificate: sudo certbot renew"
echo ""
echo -e "${GREEN}Certificate will auto-renew before expiration${NC}"
echo ""

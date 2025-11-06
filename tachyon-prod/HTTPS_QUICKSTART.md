# Quick Start: HTTPS Setup for Tachyon Subdomains

## TL;DR - 5 Minute Setup

### Prerequisites
- EC2 instance: `65.0.45.215` (ec2-65-0-45-215.ap-south-1.compute.amazonaws.com)
- Domain: `tachyon.pe` (managed in GoDaddy)
- Applications running on ports 3000 and 3001

---

## Step 1: GoDaddy DNS (2 minutes)

1. Login to [GoDaddy DNS Management](https://dcc.godaddy.com/manage/tachyon.pe/dns)
2. Add TWO A records:

| Name    | Type | Value        | TTL |
|---------|------|--------------|-----|
| relayer | A    | 65.0.45.215  | 600 |
| solver  | A    | 65.0.45.215  | 600 |

3. Save and wait 5-10 minutes for DNS propagation

---

## Step 2: EC2 Security Group (1 minute)

Ensure these ports are open:
- Port 22 (SSH)
- Port 80 (HTTP)
- Port 443 (HTTPS)
- Port 3000 (Relayer app)
- Port 3001 (Solver app)

---

## Step 3: Run Setup Script (2 minutes)

SSH into your EC2:
```bash
ssh -i your-key.pem ubuntu@65.0.45.215
```

Run the automated setup:
```bash
cd tachyon-prod/scripts
sudo ./setup-all-subdomains.sh your-email@example.com
```

**Done!** Your services will be live at:
- https://relayer.tachyon.pe
- https://solver.tachyon.pe

---

## Testing

```bash
# Test DNS resolution
nslookup relayer.tachyon.pe
nslookup solver.tachyon.pe

# Test HTTPS endpoints
curl https://relayer.tachyon.pe
curl https://solver.tachyon.pe

# Check SSL certificate
openssl s_client -connect relayer.tachyon.pe:443 -servername relayer.tachyon.pe
```

---

## Troubleshooting

### DNS not resolving?
- Wait longer (up to 30 minutes)
- Clear DNS cache: `sudo systemd-resolve --flush-caches`

### Connection refused?
- Check apps are running: `netstat -tlnp | grep -E ":(3000|3001)"`
- Check Nginx: `sudo systemctl status nginx`
- View logs: `sudo tail -f /var/log/nginx/error.log`

### SSL certificate failed?
- Ensure DNS is fully propagated first
- Check ports 80/443 are open in security group
- Retry: `sudo certbot --nginx -d relayer.tachyon.pe`

---

## Useful Commands

```bash
# Check Nginx status
sudo systemctl status nginx

# View access logs
sudo tail -f /var/log/nginx/access.log

# Reload Nginx config
sudo systemctl reload nginx

# Check SSL certificates
sudo certbot certificates

# Manual renewal
sudo certbot renew

# Check what's running on ports
sudo netstat -tlnp | grep -E ":(80|443|3000|3001)"
```

---

## What the Script Does

1. Installs Nginx and Certbot
2. Configures reverse proxy for both subdomains
3. Obtains free SSL certificates from Let's Encrypt
4. Sets up automatic certificate renewal (every 60 days)
5. Configures HTTPS redirect (HTTP → HTTPS)
6. Enables WebSocket and CORS support

---

## Manual Configuration

If you need to configure individually:

```bash
# Setup relayer only
sudo ./https-setup.sh relayer.tachyon.pe your-email@example.com 3000

# Setup solver only
sudo ./https-setup.sh solver.tachyon.pe your-email@example.com 3001
```

---

## Production Checklist

- [ ] DNS A records added in GoDaddy
- [ ] Security group allows ports 80, 443
- [ ] Applications running on ports 3000, 3001
- [ ] HTTPS setup script executed successfully
- [ ] Both domains accessible via HTTPS
- [ ] SSL certificates valid (check with browser)
- [ ] Auto-renewal configured (check: `sudo certbot renew --dry-run`)
- [ ] Monitoring/logging setup (optional)

---

For detailed documentation, see [AWS_HTTPS_SETUP.md](./AWS_HTTPS_SETUP.md)

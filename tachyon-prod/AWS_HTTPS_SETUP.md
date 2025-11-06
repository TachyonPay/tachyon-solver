# HTTPS Setup for Tachyon Subdomains

## Overview
This guide will help you:
1. Configure DNS in GoDaddy for multiple subdomains
2. Set up SSL certificates using Let's Encrypt (Certbot)
3. Configure Nginx as a reverse proxy with HTTPS

## EC2 Instance Details
- Public DNS: `ec2-65-0-45-215.ap-south-1.compute.amazonaws.com`
- IP Address: `65.0.45.215`
- Subdomains:
  - `relayer.tachyon.pe` → Port 3000
  - `solver.tachyon.pe` → Port 3001

---

## Step 1: Configure DNS in GoDaddy

### Add A Records for Both Subdomains

1. **Login to GoDaddy**
   - Go to https://www.godaddy.com
   - Navigate to "My Products" → "DNS" for `tachyon.pe`

2. **Add A Records** (Add both)

   **First Record (Relayer):**
   - Click "Add" or "Add Record"
   - **Type**: A
   - **Name**: relayer
   - **Value**: `65.0.45.215`
   - **TTL**: 600 seconds (or default)
   - Click "Save"

   **Second Record (Solver):**
   - Click "Add" or "Add Record"
   - **Type**: A
   - **Name**: solver
   - **Value**: `65.0.45.215`
   - **TTL**: 600 seconds (or default)
   - Click "Save"

3. **Wait for DNS Propagation**
   - DNS changes can take 5-30 minutes to propagate
   - Test with:
     - `nslookup relayer.tachyon.pe`
     - `nslookup solver.tachyon.pe`

---

## Step 2: Configure EC2 Security Group

Ensure your EC2 security group allows the following inbound traffic:

| Type  | Protocol | Port Range | Source    | Description          |
|-------|----------|------------|-----------|----------------------|
| SSH   | TCP      | 22         | Your IP   | SSH access           |
| HTTP  | TCP      | 80         | 0.0.0.0/0 | HTTP traffic         |
| HTTPS | TCP      | 443        | 0.0.0.0/0 | HTTPS traffic        |

**To add rules:**
1. Go to EC2 Console → Security Groups
2. Select your instance's security group
3. Click "Edit inbound rules"
4. Add rules for ports 80 and 443 if not present

---

## Step 3: Install and Configure Nginx + SSL

### Connect to Your EC2 Instance

```bash
ssh -i your-key.pem ubuntu@relayer.tachyon.pe
# or
ssh -i your-key.pem ubuntu@65.0.45.215
```

### Option A: Setup All Subdomains at Once (Recommended)

Use the automated script to configure both subdomains:

```bash
cd /path/to/tachyon-prod/scripts
chmod +x setup-all-subdomains.sh
sudo ./setup-all-subdomains.sh your-email@example.com
```

This will automatically configure:
- `relayer.tachyon.pe` → Port 3000
- `solver.tachyon.pe` → Port 3001

### Option B: Setup Subdomains Individually

Configure each subdomain separately:

```bash
cd /path/to/tachyon-prod/scripts
chmod +x https-setup.sh

# Setup relayer subdomain
sudo ./https-setup.sh relayer.tachyon.pe your-email@example.com 3000

# Setup solver subdomain
sudo ./https-setup.sh solver.tachyon.pe your-email@example.com 3001
```

**Parameters:**
- `relayer.tachyon.pe` / `solver.tachyon.pe`: Your subdomain
- `your-email@example.com`: Your email for Let's Encrypt notifications
- `3000` / `3001`: Port where your application is running

---

## Step 4: Manual Setup (Alternative)

If you prefer manual setup, follow these steps:

### 4.1 Install Nginx

```bash
sudo apt update
sudo apt install -y nginx
sudo systemctl start nginx
sudo systemctl enable nginx
```

### 4.2 Install Certbot (Let's Encrypt Client)

```bash
sudo apt install -y certbot python3-certbot-nginx
```

### 4.3 Configure Nginx for Your Application

Create Nginx configuration:

```bash
sudo nano /etc/nginx/sites-available/relayer.tachyon.pe
```

Add this configuration (replace `8080` with your app's port):

```nginx
server {
    listen 80;
    server_name relayer.tachyon.pe;

    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Enable the site:

```bash
sudo ln -s /etc/nginx/sites-available/relayer.tachyon.pe /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 4.4 Obtain SSL Certificate

```bash
sudo certbot --nginx -d relayer.tachyon.pe
```

Follow the prompts:
- Enter your email address
- Agree to terms of service
- Choose whether to redirect HTTP to HTTPS (recommended: Yes)

### 4.5 Auto-Renewal Setup

Certbot automatically sets up renewal. Test it:

```bash
sudo certbot renew --dry-run
```

---

## Step 5: Verify Setup

### Test DNS Resolution

```bash
nslookup relayer.tachyon.pe
dig relayer.tachyon.pe
```

### Test HTTP and HTTPS

```bash
curl http://relayer.tachyon.pe
curl https://relayer.tachyon.pe
```

### Check Certificate

```bash
openssl s_client -connect relayer.tachyon.pe:443 -servername relayer.tachyon.pe
```

Or visit in browser: https://relayer.tachyon.pe

---

## Troubleshooting

### DNS Not Resolving
- Wait longer (up to 48 hours, usually 5-30 minutes)
- Clear DNS cache: `sudo systemd-resolve --flush-caches` (Linux)
- Try different DNS server: `nslookup relayer.tachyon.pe 8.8.8.8`

### Certbot Fails
- Ensure DNS is fully propagated first
- Check ports 80 and 443 are open in security group
- Verify Nginx is running: `sudo systemctl status nginx`
- Check Nginx logs: `sudo tail -f /var/log/nginx/error.log`

### Connection Refused
- Check your application is running: `netstat -tlnp | grep :8080`
- Verify Nginx configuration: `sudo nginx -t`
- Check Nginx logs: `sudo journalctl -u nginx -f`

### Certificate Renewal Issues
- Check renewal timer: `sudo systemctl status certbot.timer`
- Manually renew: `sudo certbot renew`
- Check Certbot logs: `sudo cat /var/log/letsencrypt/letsencrypt.log`

---

## Firewall Configuration (if UFW is enabled)

```bash
sudo ufw allow 'Nginx Full'
sudo ufw delete allow 'Nginx HTTP'
sudo ufw status
```

---

## Maintenance

### Renew Certificate Manually (if needed)

```bash
sudo certbot renew
sudo systemctl reload nginx
```

### Check Certificate Expiry

```bash
sudo certbot certificates
```

### Update Nginx Configuration

```bash
sudo nano /etc/nginx/sites-available/relayer.tachyon.pe
sudo nginx -t
sudo systemctl reload nginx
```

---

## Quick Reference

### Useful Commands

```bash
# Check Nginx status
sudo systemctl status nginx

# Test Nginx configuration
sudo nginx -t

# Reload Nginx
sudo systemctl reload nginx

# View Nginx logs
sudo tail -f /var/log/nginx/access.log
sudo tail -f /var/log/nginx/error.log

# Check SSL certificate
sudo certbot certificates

# Renew SSL certificate
sudo certbot renew

# Check ports
sudo netstat -tlnp | grep nginx
```

---

## Security Recommendations

1. **Keep System Updated**
   ```bash
   sudo apt update && sudo apt upgrade -y
   ```

2. **Configure Firewall**
   ```bash
   sudo ufw enable
   sudo ufw allow ssh
   sudo ufw allow 'Nginx Full'
   ```

3. **Enable Fail2Ban** (optional)
   ```bash
   sudo apt install fail2ban
   sudo systemctl enable fail2ban
   ```

4. **Regular Certificate Renewal**
   - Certbot auto-renews, but verify: `sudo certbot renew --dry-run`

5. **Monitor Logs**
   - Set up log monitoring for suspicious activity
   - Consider using CloudWatch for EC2 monitoring

#!/bin/bash
set -euo pipefail

CUSTOMER="${customer_name}"
FQDN="${fqdn}"
REGION="${region}"

export DEBIAN_FRONTEND=noninteractive

# --- System packages ---
apt-get update
apt-get upgrade -y
apt-get install -y nginx certbot python3-certbot-nginx jq unzip

# --- AWS CLI v2 (not available via apt on Ubuntu 24.04) ---
curl -s "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
cd /tmp && unzip -qo awscliv2.zip && ./aws/install
rm -rf /tmp/aws /tmp/awscliv2.zip

# --- Vega user ---
useradd --system --create-home --shell /bin/bash vega
mkdir -p /home/vega/.vega
chown -R vega:vega /home/vega/.vega

# --- Pull env from SSM ---
aws ssm get-parameter \
  --name "/vega/$${CUSTOMER}/env" \
  --with-decryption \
  --region "$${REGION}" \
  --query 'Parameter.Value' \
  --output text > /home/vega/.vega/env
chown vega:vega /home/vega/.vega/env
chmod 600 /home/vega/.vega/env

# --- Systemd service ---
cat > /etc/systemd/system/vega.service <<'UNIT'
[Unit]
Description=Vega AI Agent (${customer_name})
After=network.target

[Service]
Type=simple
User=vega
WorkingDirectory=/home/vega
EnvironmentFile=/home/vega/.vega/env
ExecStart=/usr/local/bin/vega serve /home/vega/.vega/config.vega.yaml --addr 0.0.0.0:3001
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable vega.service
# Don't start yet — binary hasn't been deployed

# --- Nginx reverse proxy ---
cat > /etc/nginx/sites-available/vega <<NGINX
server {
    listen 80;
    server_name $${FQDN};

    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 86400;
        proxy_send_timeout 86400;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/vega /etc/nginx/sites-enabled/vega
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx

# --- Let's Encrypt ---
certbot --nginx --non-interactive --agree-tos \
  -m admin@v3ga.dev \
  -d "$${FQDN}"

echo "Bootstrap complete for $${FQDN}"

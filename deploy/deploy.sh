#!/usr/bin/env bash
# deploy.sh — Deploy Slacker to r3x.io
# Run as root on the server: bash deploy.sh
set -euo pipefail

APP=/opt/slacker
REPO=git@github.com:ev-claw/slacker.git
SERVICE=slacker
CADDY_CONF=/etc/caddy/conf.d/slacker.caddy

echo "==> Deploying Slacker..."

# ── Clone or pull ─────────────────────────────────────────────────────────────
if [ -d "$APP/.git" ]; then
  echo "--> Pulling latest..."
  runuser -u deploy -- git -C "$APP" pull --ff-only
else
  echo "--> Cloning repo..."
  runuser -u deploy -- sh -c "GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=accept-new' git clone $REPO $APP"
fi

chown -R www-data:www-data "$APP"

# ── Install/update systemd service ───────────────────────────────────────────
echo "--> Installing systemd service..."
cp "$APP/deploy/slacker.service" /etc/systemd/system/slacker.service
systemctl daemon-reload
systemctl enable "$SERVICE"
systemctl restart "$SERVICE"
sleep 2
systemctl is-active --quiet "$SERVICE" && echo "--> Service running ✓" || { echo "ERROR: Service failed to start"; journalctl -u "$SERVICE" -n 20; exit 1; }

# ── Caddy configuration ───────────────────────────────────────────────────────
echo "--> Configuring Caddy..."
cat > "$CADDY_CONF" <<"CONF"
slacker.r3x.io {
    reverse_proxy localhost:8391
    encode gzip
}
CONF

caddy fmt --overwrite "$CADDY_CONF"
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy

echo ""
echo "✅ Slacker deployed to https://slacker.r3x.io"
echo "   Health check: https://slacker.r3x.io/health"

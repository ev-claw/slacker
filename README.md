# Slacker

A real-time Slack catch-up activity feed. Messages from connected Slack channels stream in via Server-Sent Events and are displayed in a chronological feed with per-channel filtering.

## Live

**https://slacker.r3x.io**

## Architecture

```
Slack → POST /webhook/slack → Go server → SSE /events → Browser feed
                                   ↑
                      mock generator (MOCK_EVENTS=true)
```

- **`main.go`** — Go HTTP server (zero external dependencies)
- **`public/index.html`** — Single-file frontend with embedded CSS/JS
- **`deploy/`** — Caddy config, systemd unit reference, deploy script

## Local development

```bash
go run .
# or with mock events explicitly on:
MOCK_EVENTS=true go run .
# open http://localhost:8391
```

Mock messages fire every 10 seconds so you can watch the feed populate. Six pre-seeded messages appear on first load.

## Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/` | GET | Activity feed UI |
| `/events` | GET | SSE stream (real-time messages) |
| `/api/history` | GET | Recent message history (JSON) |
| `/webhook/slack` | POST | Slack Events API receiver |
| `/health` | GET | Health check — returns client count and message count |

## Wiring real Slack channels (next step)

### 1. Create a Slack app

1. Go to https://api.slack.com/apps → **Create New App** → **From Scratch**
2. Name it "Slacker" and select your workspace
3. Under **Event Subscriptions**, enable events and set the Request URL to:
   ```
   https://slacker.r3x.io/webhook/slack
   ```
   Slack will POST a `url_verification` challenge — the server handles this automatically.
4. Subscribe to **bot events**:
   - `message.channels` — public channel messages
   - `message.groups` — private channel messages (optional)
   - `app_mention` — @mentions of the bot
5. Under **OAuth & Permissions**, add bot scopes: `channels:history`, `channels:read`
6. Install the app to your workspace and note the **Signing Secret** from Basic Information

### 2. Update the systemd service

Edit `/etc/systemd/system/slacker.service` on the server and add:

```ini
Environment=SLACK_SIGNING_SECRET=<your-signing-secret>
Environment=MOCK_EVENTS=false
```

Then reload:

```bash
systemctl daemon-reload && systemctl restart slacker
```

### 3. Invite the bot to channels

In each Slack channel you want to monitor:
```
/invite @Slacker
```

Messages will now stream into the feed in real time.

## Deployment

The server uses the standard `r3x-deploy-go-repo` workflow. On the server as root:

```bash
r3x-deploy-go-repo slacker slacker.r3x.io git@github.com:ev-claw/slacker.git main
```

Or just run `deploy/deploy.sh`. The script clones/pulls the repo, compiles the Go binary, installs the systemd service, and configures Caddy.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8391` | Server listen port |
| `HOST` | _(empty)_ | Bind address (e.g. `127.0.0.1`) |
| `MOCK_EVENTS` | `true` | Enable mock event generator |
| `SLACK_SIGNING_SECRET` | _(empty)_ | Slack app signing secret for webhook verification |

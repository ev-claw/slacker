# Slacker

A real-time Slack catch-up activity feed. Messages from connected Slack channels stream in via Server-Sent Events and are displayed in a chronological feed with per-channel filtering.

## Live

**https://slacker.r3x.io**

## Architecture

```
Slack → POST /webhook/slack → Node.js server → SSE /events → Browser feed
                                                ↑
                             mock generator (MOCK_EVENTS=true)
```

- **`server.js`** — Vanilla Node.js HTTP server (no external dependencies)
- **`public/index.html`** — Single-file frontend with embedded CSS/JS
- **`deploy/`** — Caddy config, systemd unit, deploy script

## Local development

```bash
npm run dev        # starts server on :8391 with mock events enabled
# then open http://localhost:8391
```

Mock messages fire every 10 seconds so you can watch the feed populate.

## Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/` | GET | Activity feed UI |
| `/events` | GET | SSE stream (real-time messages) |
| `/api/history` | GET | Recent message history (JSON) |
| `/webhook/slack` | POST | Slack Events API receiver |
| `/health` | GET | Health check |

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
5. Under **OAuth & Permissions**, add scopes: `channels:history`, `channels:read`
6. Install the app to your workspace

### 2. Configure the server

Copy `.env.example` to `.env` on the server and fill in:

```bash
SLACK_SIGNING_SECRET=<from Slack app Basic Information page>
MOCK_EVENTS=false
```

Then restart the service:

```bash
systemctl restart slacker
```

### 3. Invite the bot to channels

In each Slack channel you want to monitor:
```
/invite @Slacker
```

Messages will now stream into the feed immediately.

## Deployment

```bash
# On the server as root:
bash /opt/slacker/deploy/deploy.sh
```

The script clones/pulls the repo, installs the systemd service, and configures Caddy.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8391` | Server listen port |
| `MOCK_EVENTS` | `true` | Enable mock event generator |
| `SLACK_SIGNING_SECRET` | _(empty)_ | Slack app signing secret for webhook verification |
| `SLACK_BOT_TOKEN` | _(empty)_ | Bot OAuth token (reserved for future use) |

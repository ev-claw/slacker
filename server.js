'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const PORT = parseInt(process.env.PORT || '8391', 10);
const SLACK_SIGNING_SECRET = process.env.SLACK_SIGNING_SECRET || '';
const MOCK_EVENTS = process.env.MOCK_EVENTS !== 'false';

const PUBLIC_DIR = path.join(__dirname, 'public');

// ── In-memory message ring buffer ───────────────────────────────────────────
const MAX_HISTORY = 200;
const history = [];

// ── SSE client registry ─────────────────────────────────────────────────────
const clients = new Set();

function broadcast(event) {
  const payload = `data: ${JSON.stringify(event)}\n\n`;
  for (const res of clients) {
    try { res.write(payload); } catch (_) { clients.delete(res); }
  }
}

function addEvent(event) {
  event.id = crypto.randomUUID();
  event.ts = event.ts || new Date().toISOString();
  history.push(event);
  if (history.length > MAX_HISTORY) history.shift();
  broadcast(event);
  console.log(`[event] #${event.channel} <${event.user}> ${event.text.slice(0, 60)}`);
}

// ── Mock event generator ─────────────────────────────────────────────────────
const MOCK_USERS = ['alice', 'bob', 'charlie', 'dana', 'eve', 'frank'];
const MOCK_CHANNELS = ['general', 'engineering', 'random', 'design', 'ops'];
const MOCK_MESSAGES = [
  'Hey team, standup in 5 ✋',
  'PR is ready for review — check the link in the thread',
  'Just deployed the new release to prod 🚀',
  "Anyone free for a quick call? I'm blocked on the auth issue",
  'Updated the docs with the new API changes',
  'Heads up: staging is down for maintenance until 3pm',
  'Coffee chat at 2pm? ☕',
  'The build is green again 🟢',
  'Left a few comments on the design doc',
  'OOO today, back tomorrow',
  'Just pushed a hotfix for the login bug',
  'Sprint planning reminder: tomorrow 10am',
  '+1 to that, great idea',
  'Looking into it now...',
  'Fixed! Turned out to be a race condition 🐛',
  'Metrics look good post-deploy, no errors in Sentry',
  'Who owns the billing service? Quick Q',
  'Reminder: retro at 4pm 📝',
  'Database migration completed successfully ✅',
  'Has anyone seen flakiness in the CI pipeline today?',
];

// Pre-seed a few mock messages so the feed is not empty on first load
if (MOCK_EVENTS) {
  const now = Date.now();
  for (let i = 5; i >= 0; i--) {
    addEvent({
      type: 'message',
      channel: MOCK_CHANNELS[Math.floor(Math.random() * MOCK_CHANNELS.length)],
      user: MOCK_USERS[Math.floor(Math.random() * MOCK_USERS.length)],
      text: MOCK_MESSAGES[Math.floor(Math.random() * MOCK_MESSAGES.length)],
      ts: new Date(now - i * 90_000).toISOString(),
      mock: true,
    });
  }

  setInterval(() => {
    addEvent({
      type: 'message',
      channel: MOCK_CHANNELS[Math.floor(Math.random() * MOCK_CHANNELS.length)],
      user: MOCK_USERS[Math.floor(Math.random() * MOCK_USERS.length)],
      text: MOCK_MESSAGES[Math.floor(Math.random() * MOCK_MESSAGES.length)],
      mock: true,
    });
  }, 10_000);
}

// ── Slack signature verification ─────────────────────────────────────────────
function verifySlackSignature(req, rawBody) {
  if (!SLACK_SIGNING_SECRET) return true; // skip if not configured
  const timestamp = req.headers['x-slack-request-timestamp'];
  const slackSig = req.headers['x-slack-signature'];
  if (!timestamp || !slackSig) return false;
  if (Math.abs(Date.now() / 1000 - parseInt(timestamp)) > 300) return false; // replay protection
  const base = `v0:${timestamp}:${rawBody}`;
  const sig = 'v0=' + crypto.createHmac('sha256', SLACK_SIGNING_SECRET).update(base).digest('hex');
  return crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(slackSig));
}

// ── MIME types ───────────────────────────────────────────────────────────────
const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript',
  '.css': 'text/css',
  '.json': 'application/json',
  '.ico': 'image/x-icon',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
};

// ── HTTP server ───────────────────────────────────────────────────────────────
const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);
  const pathname = url.pathname;

  // ── SSE stream ─────────────────────────────────────────────────────────────
  if (pathname === '/events' && req.method === 'GET') {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'Connection': 'keep-alive',
      'X-Accel-Buffering': 'no', // disable Nginx/Caddy buffering
    });
    res.write(': connected\n\n');

    // Send full history on connect
    const init = { type: 'history', messages: history };
    res.write(`data: ${JSON.stringify(init)}\n\n`);

    clients.add(res);

    // Heartbeat to keep connection alive through proxies
    const hb = setInterval(() => {
      try { res.write(': ping\n\n'); } catch (_) { clearInterval(hb); clients.delete(res); }
    }, 25_000);

    req.on('close', () => { clearInterval(hb); clients.delete(res); });
    return;
  }

  // ── History API ────────────────────────────────────────────────────────────
  if (pathname === '/api/history' && req.method === 'GET') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(history));
    return;
  }

  // ── Health check ───────────────────────────────────────────────────────────
  if (pathname === '/health' && req.method === 'GET') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ status: 'ok', clients: clients.size, messages: history.length }));
    return;
  }

  // ── Slack Events API webhook ───────────────────────────────────────────────
  if (pathname === '/webhook/slack' && req.method === 'POST') {
    let rawBody = '';
    req.on('data', chunk => rawBody += chunk);
    req.on('end', () => {
      if (!verifySlackSignature(req, rawBody)) {
        res.writeHead(401);
        res.end('Unauthorized');
        return;
      }

      let payload;
      try { payload = JSON.parse(rawBody); }
      catch {
        res.writeHead(400);
        res.end('Bad request');
        return;
      }

      // Slack URL verification challenge (one-time setup)
      if (payload.type === 'url_verification') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ challenge: payload.challenge }));
        return;
      }

      // Slack event callback
      if (payload.type === 'event_callback') {
        const evt = payload.event || {};
        if (['message', 'app_mention'].includes(evt.type) && !evt.bot_id) {
          addEvent({
            type: evt.type,
            channel: evt.channel || payload.event?.channel || 'unknown',
            user: evt.user || evt.username || 'unknown',
            text: evt.text || '',
            slack_ts: evt.ts,
            subtype: evt.subtype || null,
            team: payload.team_id || null,
          });
        }
      }

      res.writeHead(200);
      res.end('OK');
    });
    return;
  }

  // ── Static file serving ────────────────────────────────────────────────────
  let filePath = path.join(PUBLIC_DIR, pathname === '/' ? 'index.html' : pathname);
  if (!filePath.startsWith(PUBLIC_DIR + path.sep) && filePath !== PUBLIC_DIR) {
    res.writeHead(403);
    res.end();
    return;
  }

  const ext = path.extname(filePath);
  const contentType = MIME[ext] || 'application/octet-stream';
  const isHtml = ext === '.html' || !ext;

  fs.readFile(filePath, (err, data) => {
    if (err) {
      res.writeHead(err.code === 'ENOENT' ? 404 : 500);
      res.end(err.code === 'ENOENT' ? 'Not found' : 'Server error');
      return;
    }
    res.writeHead(200, {
      'Content-Type': contentType,
      'Cache-Control': isHtml ? 'no-cache' : 'public, max-age=3600',
    });
    res.end(data);
  });
});

server.listen(PORT, () => {
  console.log(`[slacker] listening on :${PORT}  mock=${MOCK_EVENTS}  secret=${SLACK_SIGNING_SECRET ? 'set' : 'not set'}`);
});

package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"sync"
	"time"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type Message struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Channel  string `json:"channel"`
	User     string `json:"user"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	SlackTS  string `json:"slack_ts,omitempty"`
	Subtype  string `json:"subtype,omitempty"`
	Team     string `json:"team,omitempty"`
	Mock     bool   `json:"mock,omitempty"`
}

type SSEPayload struct {
	Type     string    `json:"type"`
	Messages []Message `json:"messages,omitempty"`
	// embedded message fields (for single-event payloads)
	ID      string `json:"id,omitempty"`
	Channel string `json:"channel,omitempty"`
	User    string `json:"user,omitempty"`
	Text    string `json:"text,omitempty"`
	TS      string `json:"ts,omitempty"`
	Mock    bool   `json:"mock,omitempty"`
}

// ── State ─────────────────────────────────────────────────────────────────────

const maxHistory = 200

var (
	mu       sync.RWMutex
	history  []Message
	clients  = map[chan []byte]struct{}{}
)

func newID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func addMessage(m Message) {
	if m.ID == "" {
		m.ID = newID()
	}
	if m.TS == "" {
		m.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}

	mu.Lock()
	history = append(history, m)
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	payload := SSEPayload{
		Type:    m.Type,
		ID:      m.ID,
		Channel: m.Channel,
		User:    m.User,
		Text:    m.Text,
		TS:      m.TS,
		Mock:    m.Mock,
	}
	data, _ := json.Marshal(payload)
	line := append([]byte("data: "), data...)
	line = append(line, '\n', '\n')
	for ch := range clients {
		select {
		case ch <- line:
		default: // skip slow clients
		}
	}
	mu.Unlock()

	log.Printf("[event] #%s <%s> %s", m.Channel, m.User, truncate(m.Text, 60))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── Mock events ───────────────────────────────────────────────────────────────

var mockUsers    = []string{"alice", "bob", "charlie", "dana", "eve", "frank"}
var mockChannels = []string{"general", "engineering", "random", "design", "ops"}
var mockMessages = []string{
	"Hey team, standup in 5 ✋",
	"PR is ready for review — check the thread",
	"Just deployed the new release to prod 🚀",
	"Anyone free for a quick call? I'm blocked on the auth issue",
	"Updated the docs with the new API changes",
	"Heads up: staging is down for maintenance until 3pm",
	"Coffee chat at 2pm? ☕",
	"The build is green again 🟢",
	"Left a few comments on the design doc",
	"OOO today, back tomorrow",
	"Just pushed a hotfix for the login bug",
	"Sprint planning reminder: tomorrow 10am",
	"+1 to that, great idea",
	"Looking into it now...",
	"Fixed! Turned out to be a race condition 🐛",
	"Metrics look good post-deploy, no errors",
	"Who owns the billing service? Quick Q",
	"Reminder: retro at 4pm 📝",
	"Database migration completed successfully ✅",
	"Has anyone seen flakiness in CI today?",
}

func randChoice(s []string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(s))))
	return s[n.Int64()]
}

func seedMockHistory() {
	now := time.Now()
	for i := 5; i >= 0; i-- {
		m := Message{
			Type:    "message",
			Channel: randChoice(mockChannels),
			User:    randChoice(mockUsers),
			Text:    randChoice(mockMessages),
			TS:      now.Add(-time.Duration(i) * 90 * time.Second).UTC().Format(time.RFC3339Nano),
			Mock:    true,
		}
		m.ID = newID()
		mu.Lock()
		history = append(history, m)
		mu.Unlock()
	}
}

func runMockGenerator() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		addMessage(Message{
			Type:    "message",
			Channel: randChoice(mockChannels),
			User:    randChoice(mockUsers),
			Text:    randChoice(mockMessages),
			Mock:    true,
		})
	}
}

// ── Slack signature verification ──────────────────────────────────────────────

func verifySlack(r *http.Request, rawBody []byte, secret string) bool {
	if secret == "" {
		return true
	}
	tsStr := r.Header.Get("X-Slack-Request-Timestamp")
	sigHeader := r.Header.Get("X-Slack-Signature")
	if tsStr == "" || sigHeader == "" {
		return false
	}
	// Replay protection: reject requests older than 5 minutes
	var tsUnix int64
	fmt.Sscanf(tsStr, "%d", &tsUnix)
	if time.Since(time.Unix(tsUnix, 0)).Abs() > 5*time.Minute {
		return false
	}
	base := fmt.Sprintf("v0:%s:%s", tsStr, string(rawBody))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base)) //nolint:errcheck
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sigHeader))
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial comment
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Send history snapshot
	mu.RLock()
	snap := make([]Message, len(history))
	copy(snap, history)
	mu.RUnlock()

	initPayload := struct {
		Type     string    `json:"type"`
		Messages []Message `json:"messages"`
	}{"history", snap}
	data, _ := json.Marshal(initPayload)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Register client
	ch := make(chan []byte, 32)
	mu.Lock()
	clients[ch] = struct{}{}
	mu.Unlock()

	defer func() {
		mu.Lock()
		delete(clients, ch)
		mu.Unlock()
	}()

	// Heartbeat ticker
	hb := time.NewTicker(25 * time.Second)
	defer hb.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case payload, ok := <-ch:
			if !ok {
				return
			}
			w.Write(payload) //nolint:errcheck
			flusher.Flush()
		}
	}
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	snap := make([]Message, len(history))
	copy(snap, history)
	mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snap) //nolint:errcheck
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	nClients := len(clients)
	nMsg := len(history)
	mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","clients":%d,"messages":%d}`+"\n", nClients, nMsg)
}

func handleWebhook(w http.ResponseWriter, r *http.Request, secret string) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if !verifySlack(r, rawBody, secret) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload struct {
		Type  string `json:"type"`
		TeamID string `json:"team_id"`
		// url_verification
		Challenge string `json:"challenge"`
		// event_callback
		Event struct {
			Type     string `json:"type"`
			Channel  string `json:"channel"`
			User     string `json:"user"`
			Username string `json:"username"`
			Text     string `json:"text"`
			TS       string `json:"ts"`
			BotID    string `json:"bot_id"`
			Subtype  string `json:"subtype"`
		} `json:"event"`
	}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if payload.Type == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": payload.Challenge}) //nolint:errcheck
		return
	}

	if payload.Type == "event_callback" {
		evt := payload.Event
		if (evt.Type == "message" || evt.Type == "app_mention") && evt.BotID == "" {
			user := evt.User
			if user == "" {
				user = evt.Username
			}
			if user == "" {
				user = "unknown"
			}
			addMessage(Message{
				Type:    evt.Type,
				Channel: evt.Channel,
				User:    user,
				Text:    evt.Text,
				SlackTS: evt.TS,
				Subtype: evt.Subtype,
				Team:    payload.TeamID,
			})
		}
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8391"
	}
	host := os.Getenv("HOST")
	addr := host + ":" + port

	slackSecret := os.Getenv("SLACK_SIGNING_SECRET")
	mockEvents := os.Getenv("MOCK_EVENTS") != "false"

	if mockEvents {
		seedMockHistory()
		go runMockGenerator()
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleSSE(w, r)
	})

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		handleHistory(w, r)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(w, r)
	})

	mux.HandleFunc("/webhook/slack", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleWebhook(w, r, slackSecret)
	})

	// Static files from ./public/
	mux.Handle("/", http.FileServer(http.Dir("./public")))

	log.Printf("[slacker] listening on %s  mock=%v  secret=%v",
		addr, mockEvents, slackSecret != "")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

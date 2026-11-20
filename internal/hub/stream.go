package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/CodexCircle/Regulus/internal/auth"
)

const (
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
)

// maxStreamsPerUser bounds the open streams of one user (#244). The
// dashboard opens one per tab, so this leaves room for many tabs and
// windows while a script that keeps opening streams cannot hold the hub's
// memory and goroutines. A variable so that a test need not open many.
var maxStreamsPerUser = 16

// streamSlots counts the open streams of each user.
type streamSlots struct {
	mu   sync.Mutex
	open map[int64]int
}

// take reserves a stream for the user, or reports false when the user already
// has the most allowed. The new stream is refused rather than the oldest one
// closed: two windows of one user over the cap would otherwise throw each
// other out for ever, each reconnecting in turn.
func (s *streamSlots) take(user int64) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[user] >= maxStreamsPerUser {
		return nil, false
	}
	s.open[user]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.open[user]--; s.open[user] <= 0 {
				delete(s.open, user)
			}
		})
	}, true
}

// sessionRecheck is how often an open stream asks whether its session and
// permission still stand (a variable so that a test need not wait).
var sessionRecheck = 30 * time.Second

// streamHandler upgrades GET /api/stream to a WebSocket and pushes JSON
// events: first a "snapshot" of the connected agents, then "agent.connected",
// "agent.disconnected", "agent.heartbeat", "agent.metrics" and
// "plugins.changed" (see pluginsEvent) as they happen.
// The client sends nothing. The upgrade refuses a cross-origin page, and the
// session cookie is checked before the handler runs, and again every
// sessionRecheck: a stream whose session ended (logout, disabled account, new
// password) or whose user lost the permission is closed. still makes that
// check for one request; nil means no login is needed. A user has at most
// maxStreamsPerUser streams open; one more is refused with 429.
func streamHandler(src AgentSource, still func(*http.Request) func(context.Context) bool) http.HandlerFunc {
	slots := &streamSlots{open: map[int64]int{}}
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			writeJSONError(w, http.StatusNotFound, "stream is not available")
			return
		}
		// Without a login (tests of the handler alone) there is no user to count.
		if user, ok := auth.UserFrom(r.Context()); ok {
			release, ok := slots.take(user.ID)
			if !ok {
				// A stream that died unnoticed is gone after one ping.
				w.Header().Set("Retry-After", strconv.Itoa(int(pingInterval.Seconds())))
				writeJSONError(w, http.StatusTooManyRequests, "too many open streams for this user: close another tab or window")
				return
			}
			defer release()
		}
		// Subscribe before taking the snapshot so no event falls in between;
		// an event already included in the snapshot is harmless to repeat.
		events, cancel := src.Subscribe()
		defer cancel()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept has already answered
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := conn.CloseRead(r.Context()) // handles pings and the close handshake

		now, staleAfter := time.Now(), src.StaleAfter()
		agents := []agentJSON{}
		for _, a := range src.Agents() {
			agents = append(agents, toAgentJSON(a, now, staleAfter))
		}
		first, _ := json.Marshal(snapshotEvent{Type: "snapshot", StaleAfterSeconds: staleAfter.Seconds(), Agents: agents})
		if !send(ctx, conn, first) {
			return
		}

		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		allowed := func(context.Context) bool { return true }
		if still != nil {
			allowed = still(r)
		}
		recheck := time.NewTicker(sessionRecheck)
		defer recheck.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-recheck.C:
				if !allowed(ctx) {
					_ = conn.Close(websocket.StatusPolicyViolation, "session ended")
					return
				}
			case msg, ok := <-events:
				if !ok { // too slow: close so that the client reconnects and resyncs
					_ = conn.Close(websocket.StatusPolicyViolation, "too slow")
					return
				}
				if !send(ctx, conn, msg) {
					return
				}
			case <-ping.C:
				pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pingCtx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}
}

func send(ctx context.Context, conn *websocket.Conn, msg []byte) bool {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, msg) == nil
}

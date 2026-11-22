package hub

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// What the hub records about agent presence (agents.last_seen_at and what an
// agent reports about itself, the end of its certificate) is written here, by
// one goroutine, and never on the path of an agent connection: the handshake
// and the streams do no database I/O of their own, so a database that is
// slow, locked or down (PostgreSQL) cannot hold an agent before its Welcome.
// Writes are coalesced per agent and kind (the latest wins) and each has a
// timeout. While the hub runs, connected agents are recorded as seen every
// seenFlushEvery, so after a crash or a power cut last_seen_at is at most that
// old; Close records them once more before the database closes.

const (
	// seenFlushEvery is how often the connected agents are recorded as seen.
	seenFlushEvery = time.Minute
	// seenWriteTimeout bounds one write; a write that times out is logged
	// and dropped (the next flush records the agent again).
	seenWriteTimeout = 5 * time.Second
)

// seenWrite is one pending write: what to do, and the time it describes.
type seenWrite func(ctx context.Context) error

type seenRecorder struct {
	log       *slog.Logger
	every     time.Duration
	timeout   time.Duration
	connected func() []AgentInfo // the agents to record at each flush
	record    func(info AgentInfo, at time.Time) seenWrite

	mu      sync.Mutex
	pending map[string]seenWrite // by kind and agent id
	order   []string             // keys of pending, oldest first
	closed  bool

	failing bool // the last write failed (writer goroutine only)
	// onWrite, if set, is told how long each write took and whether it worked
	// (GET /metrics, #389). Writer goroutine only.
	onWrite func(took time.Duration, err error)

	wake      chan struct{}
	closeOnce sync.Once
	stop      chan struct{}
	done      chan struct{}
}

func newSeenRecorder(log *slog.Logger, connected func() []AgentInfo, record func(AgentInfo, time.Time) seenWrite) *seenRecorder {
	return &seenRecorder{
		log:       log,
		every:     seenFlushEvery,
		timeout:   seenWriteTimeout,
		connected: connected,
		record:    record,
		pending:   map[string]seenWrite{},
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// start runs the writer until close.
func (r *seenRecorder) start() {
	go r.run()
}

// seen queues the record that the agent was seen at at. It never waits.
func (r *seenRecorder) seen(info AgentInfo, at time.Time) {
	r.put("seen/"+info.ID, r.record(info, at))
}

// put queues w under key, replacing a write still pending under the same key.
// After close it does nothing: the final flush has already run.
func (r *seenRecorder) put(key string, w seenWrite) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if _, ok := r.pending[key]; !ok {
		r.order = append(r.order, key)
	}
	r.pending[key] = w
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *seenRecorder) run() {
	defer close(r.done)
	tick := time.NewTicker(r.every)
	defer tick.Stop()
	for {
		select {
		case <-r.wake:
		case <-tick.C:
			r.flushConnected()
		case <-r.stop:
			r.flushConnected()
			r.drain()
			return
		}
		r.drain()
	}
}

// flushConnected queues a "seen now" for every connected agent.
func (r *seenRecorder) flushConnected() {
	now := time.Now()
	for _, a := range r.connected() {
		r.mu.Lock()
		key := "seen/" + a.ID
		if _, ok := r.pending[key]; !ok {
			r.order = append(r.order, key)
		}
		r.pending[key] = r.record(a, now)
		r.mu.Unlock()
	}
}

// drain runs the pending writes in the order they were first queued.
func (r *seenRecorder) drain() {
	for {
		r.mu.Lock()
		if len(r.order) == 0 {
			r.mu.Unlock()
			return
		}
		key := r.order[0]
		r.order = r.order[1:]
		w := r.pending[key]
		delete(r.pending, key)
		r.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		began := time.Now()
		err := w(ctx)
		cancel()
		if r.onWrite != nil {
			r.onWrite(time.Since(began), err)
		}
		// One line when writes start failing and one when they work again,
		// not one per agent and minute during an outage.
		switch {
		case err != nil && !r.failing:
			r.failing = true
			r.log.Warn("cannot record when agents were seen; retrying every flush", "what", key, "err", err)
		case err == nil && r.failing:
			r.failing = false
			r.log.Info("recording when agents were seen works again")
		}
	}
}

// close records the connected agents once more, writes what is pending, and
// stops the writer. Later records are dropped. The hub calls it before it
// closes the database.
func (r *seenRecorder) close() {
	r.closeOnce.Do(func() {
		close(r.stop)
		<-r.done
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
	})
}

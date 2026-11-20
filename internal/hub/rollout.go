// rollout.go updates the connected agents to the hub's version, a wave at a time
// (StartRollout, StopRollout, runRollout, updateAgent, autoRollout), and at the
// end serves the release files that agents fetch (agentServer.FetchUpdate). The
// agent side is internal/agent/update.go. See docs/code-map.md.

package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"github.com/CodexCircle/Regulus/internal/update"
)

// Errors of the rollout.
var (
	ErrRolloutRunning = errors.New("an update of the servers is already running")
	ErrNothingToRoll  = errors.New("no connected server runs an older version than this hub")
	ErrNotRelease     = errors.New("this hub is not a release build, so it has no release to offer the servers")

	errBadRollout = errors.New("invalid rollout")
)

// Audit events of the rollout.
const (
	EventRolloutStarted  = "update_rollout_started"
	EventRolloutFinished = "update_rollout_finished"
	EventAgentUpdated    = "update_agent_updated"
	EventAgentFailed     = "update_agent_failed"
)

// Status of one server in a rollout.
const (
	AgentPending    = "pending"
	AgentOffered    = "offered"    // the update was sent; the server is fetching it
	AgentInstalling = "installing" // handed to the installer; waiting for it to come back
	AgentUpdated    = "updated"
	AgentFailed     = "failed"
	AgentSkipped    = "skipped" // an earlier failure stopped the rollout
	// AgentUnknown: the hub restarted while it waited for this server, so it
	// never saw the result (#211).
	AgentUnknown = "unknown"
)

// State of a rollout.
const (
	RolloutIdle    = "idle"
	RolloutRunning = "running"
	RolloutDone    = "done"
	RolloutFailed  = "failed" // stopped by a failure
	RolloutStopped = "stopped"
)

// RolloutAgent is a server in a rollout, or one that could be in the next.
type RolloutAgent struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname,omitempty"`
	From     string `json:"from"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

// RolloutStatus is the state of updating the servers to the version of this
// hub.
type RolloutStatus struct {
	// Target is the version the servers are updated to: the one this hub runs.
	Target string `json:"target"`
	State  string `json:"state"`
	// Candidates are the connected servers that run an older version and would
	// be updated by a new rollout.
	Candidates []RolloutAgent `json:"candidates"`
	// Agents is what the last rollout did.
	Agents     []RolloutAgent `json:"agents"`
	WaveSize   int            `json:"wave_size,omitempty"`
	StartedBy  string         `json:"started_by,omitempty"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

// RolloutOptions say what a rollout does.
type RolloutOptions struct {
	// Agents limits the rollout to these servers; empty means every candidate.
	Agents []string `json:"agents"`
	// WaveSize is how many servers are updated at the same time (default 1).
	// The rollout stops at the first failure.
	WaveSize int `json:"wave_size"`
}

// RolloutControl is what the HTTP API needs to update the servers. *Hub
// implements it.
type RolloutControl interface {
	RolloutStatus(ctx context.Context) (RolloutStatus, error)
	StartRollout(ctx context.Context, by string, opts RolloutOptions) (RolloutStatus, error)
	StopRollout(ctx context.Context, by string) (RolloutStatus, error)
}

type rolloutRun struct {
	status RolloutStatus
	cancel context.CancelFunc
	// done is closed when the goroutine that runs the rollout returns. Each run
	// has its own, so waiting for one never races with the start of the next
	// (a shared WaitGroup did: Wait in StopRollout against Add in
	// StartRollout).
	done chan struct{}
}

type rolloutState struct {
	mu   sync.Mutex
	run  *rolloutRun
	base context.Context
	// persistMu orders the writes of the run's state to the database.
	persistMu sync.Mutex

	// failedTarget is the version for which a rollout failed; the automatic
	// mode does not try that again by itself. It is kept in the database, so
	// a restart of the hub does not forget it (#211).
	failedTarget string

	// How long a server gets to fetch the update, and then to come back with
	// the new version. Replaced in tests.
	offerTimeout, returnTimeout time.Duration
	// settle is how long a server must stay connected with the new version
	// before it counts as updated: the helper on the server keeps watching the
	// new agent for its Stable period (15 s, polled every 2 s) after it
	// connects, and rolls it back if it does not stay up (#211).
	settle time.Duration
}

// defaultSettle is the helper's Stable period with a margin for its polling
// and for the agent's reconnection.
const defaultSettle = 30 * time.Second

func newRolloutState() *rolloutState {
	return &rolloutState{base: context.Background(), offerTimeout: 10 * time.Minute, returnTimeout: 10 * time.Minute, settle: defaultSettle}
}

func (h *Hub) candidates() []AgentInfo {
	var out []AgentInfo
	for _, a := range h.agents.agents() {
		if a.ID == BuiltinAgentID {
			continue
		}
		// Too-old agents are connected only to be updated (UpdateOnly), and
		// they are taken straight to the hub's version, whatever they skipped.
		updatable := a.Compat == update.CompatBehind || (a.Compat == update.CompatTooOld && a.UpdateOnly)
		if !updatable {
			continue
		}
		if a.System.GetOs() != "linux" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func rolloutAgent(a AgentInfo, status string) RolloutAgent {
	return RolloutAgent{ID: a.ID, Hostname: a.System.GetHostname(), From: a.Version, Status: status}
}

// RolloutStatus reports the servers that could be updated and what the last
// rollout did.
func (h *Hub) RolloutStatus(_ context.Context) (RolloutStatus, error) {
	h.rollout.mu.Lock()
	defer h.rollout.mu.Unlock()
	return h.snapshotLocked(), nil
}

func (h *Hub) snapshotLocked() RolloutStatus {
	st := RolloutStatus{Target: h.version, State: RolloutIdle, Candidates: []RolloutAgent{}, Agents: []RolloutAgent{}}
	if run := h.rollout.run; run != nil {
		st = run.status
		st.Agents = slices.Clone(run.status.Agents)
		st.Target = h.version
		if run.status.StartedAt != nil {
			t := *run.status.StartedAt
			st.StartedAt = &t
		}
		if run.status.FinishedAt != nil {
			t := *run.status.FinishedAt
			st.FinishedAt = &t
		}
	}
	st.Candidates = []RolloutAgent{}
	for _, a := range h.candidates() {
		st.Candidates = append(st.Candidates, rolloutAgent(a, AgentPending))
	}
	return st
}

func (h *Hub) rolloutRunning() bool {
	return h.rollout.run != nil && h.rollout.run.status.State == RolloutRunning
}

// StartRollout updates the connected servers that run an older version to the
// version of this hub, a wave at a time, and stops at the first failure.
func (h *Hub) StartRollout(ctx context.Context, by string, opts RolloutOptions) (RolloutStatus, error) {
	if _, ok := update.ParseBuildVersion(h.version); !ok {
		return RolloutStatus{}, ErrNotRelease
	}
	if opts.WaveSize < 0 || opts.WaveSize > 100 {
		return RolloutStatus{}, fmt.Errorf("%w: wave_size must be between 1 and 100", errBadRollout)
	}
	if opts.WaveSize == 0 {
		opts.WaveSize = 1
	}

	h.rollout.mu.Lock()
	if h.rolloutRunning() {
		h.rollout.mu.Unlock()
		return RolloutStatus{}, ErrRolloutRunning
	}
	h.rollout.mu.Unlock()

	var chosen []AgentInfo
	for _, a := range h.candidates() {
		if len(opts.Agents) == 0 || slices.Contains(opts.Agents, a.ID) {
			chosen = append(chosen, a)
		}
	}
	if len(chosen) == 0 {
		return RolloutStatus{}, ErrNothingToRoll
	}
	var plats []update.Platform
	for _, a := range chosen {
		p := update.Platform{OS: a.System.GetOs(), Arch: a.System.GetArch()}
		if !slices.Contains(plats, p) {
			plats = append(plats, p)
		}
	}
	if err := h.updates.EnsureRelease(ctx, h.version, plats); err != nil {
		return RolloutStatus{}, err
	}

	h.rollout.mu.Lock()
	defer h.rollout.mu.Unlock()
	if h.rolloutRunning() {
		return RolloutStatus{}, ErrRolloutRunning
	}
	now := time.Now().UTC()
	runCtx, cancel := context.WithCancel(h.rollout.base)
	run := &rolloutRun{cancel: cancel, done: make(chan struct{}), status: RolloutStatus{Target: h.version, State: RolloutRunning, WaveSize: opts.WaveSize, StartedBy: by, StartedAt: &now}}
	for _, a := range chosen {
		run.status.Agents = append(run.status.Agents, rolloutAgent(a, AgentPending))
	}
	h.rollout.run = run
	h.audit(ctx, by, EventRolloutStarted, h.version, fmt.Sprintf("%d servers, %d at a time", len(chosen), opts.WaveSize))
	h.log.Info("updating the servers", "target", h.version, "servers", len(chosen), "wave", opts.WaveSize, "by", by)

	go func() {
		defer close(run.done)
		h.runRollout(runCtx, run, by)
	}()
	return h.snapshotLocked(), nil
}

// StopRollout lets the servers already handed an update finish and offers it to
// no other.
func (h *Hub) StopRollout(ctx context.Context, by string) (RolloutStatus, error) {
	h.rollout.mu.Lock()
	run := h.rollout.run
	if run == nil || run.status.State != RolloutRunning {
		defer h.rollout.mu.Unlock()
		return h.snapshotLocked(), nil
	}
	run.status.State = RolloutStopped
	h.rollout.mu.Unlock()
	run.cancel()
	h.audit(ctx, by, EventRolloutFinished, h.version, "stopped")
	<-run.done
	return h.RolloutStatus(ctx)
}

func (h *Hub) audit(ctx context.Context, actor, event, target, detail string) {
	h.auth.Record(ctx, actor, event, target, detail)
}

func (h *Hub) setAgent(run *rolloutRun, id, status, message string) {
	h.rollout.mu.Lock()
	for i := range run.status.Agents {
		if run.status.Agents[i].ID == id {
			run.status.Agents[i].Status, run.status.Agents[i].Message = status, message
		}
	}
	h.rollout.mu.Unlock()
	h.persistRollout(run)
}

func (h *Hub) runRollout(ctx context.Context, run *rolloutRun, by string) {
	h.persistRollout(run)
	h.rollout.mu.Lock()
	ids := make([]string, len(run.status.Agents))
	for i, a := range run.status.Agents {
		ids[i] = a.ID
	}
	wave := run.status.WaveSize
	h.rollout.mu.Unlock()

	failed := false
	for start := 0; start < len(ids) && !failed && ctx.Err() == nil; start += wave {
		end := min(start+wave, len(ids))
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, id := range ids[start:end] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !h.updateAgent(ctx, run, id, by) {
					mu.Lock()
					failed = true
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
	}

	// The final state is made visible only after the audit event is written:
	// anyone who sees "done" or "failed" (the API, a test) must find
	// update_rollout_finished in the audit log. Writing it after the state
	// left a window in which it was missing.
	final := RolloutDone
	if failed {
		final = RolloutFailed
	}
	h.rollout.mu.Lock()
	stopped := run.status.State == RolloutStopped
	h.rollout.mu.Unlock()
	if !stopped {
		h.audit(context.Background(), by, EventRolloutFinished, h.version, final)
	}

	h.rollout.mu.Lock()
	for i := range run.status.Agents {
		if run.status.Agents[i].Status == AgentPending {
			run.status.Agents[i].Status = AgentSkipped
		}
	}
	finished := time.Now().UTC()
	run.status.FinishedAt = &finished
	// A stop that arrived while the event was written wins: it has its own.
	if run.status.State != RolloutStopped {
		run.status.State = final
	}
	state := run.status.State
	h.rollout.mu.Unlock()
	// The automatic mode does not try again a version that failed, whoever
	// started the rollout; one that ended well clears that.
	switch state {
	case RolloutFailed:
		h.setFailedTarget(h.version)
	case RolloutDone:
		h.setFailedTarget("")
	}
	h.persistRollout(run)
	h.log.Info("updating the servers finished", "target", h.version, "state", state)
}

// updateAgent hands the update to one server and waits for it to come back
// with the new version. It reports whether that worked.
func (h *Hub) updateAgent(ctx context.Context, run *rolloutRun, id, by string) bool {
	fail := func(msg string) bool {
		h.setAgent(run, id, AgentFailed, msg)
		h.audit(context.Background(), by, EventAgentFailed, id, msg)
		h.log.Warn("server not updated", "agent", id, "err", msg)
		return false
	}
	info, ok := h.agentInfo(id)
	if !ok {
		return fail("the server is not connected")
	}
	plat := update.Platform{OS: info.System.GetOs(), Arch: info.System.GetArch()}
	name, sha, size, err := h.updates.Files().Offer(h.version, plat)
	if err != nil {
		return fail("the hub has no archive for this server: " + err.Error())
	}
	h.setAgent(run, id, AgentOffered, "")

	// Watched from before the command: the server cannot restart earlier.
	events, unwatch := h.agents.watch(id)
	defer unwatch()
	cctx, cancel := context.WithTimeout(ctx, h.rollout.offerTimeout)
	res, err := h.agents.sendCommand(cctx, id, &agentv1.Command{Kind: &agentv1.Command_ApplyUpdate{ApplyUpdate: &agentv1.ApplyUpdate{
		Version: h.version, Archive: name, Sha256: sha, Size: uint64(size),
	}}})
	cancel()
	switch {
	case ctx.Err() != nil:
		return fail("stopped")
	case err != nil:
		return fail(err.Error())
	case !res.GetSuccess() && res.GetError() == "unsupported command":
		return fail("this agent is too old to update itself: update it once with install.sh")
	case !res.GetSuccess():
		return fail(res.GetError())
	}
	h.setAgent(run, id, AgentInstalling, "")
	return h.awaitReturn(ctx, run, info, by, events, fail)
}

// awaitReturn waits for a server that handed the update to its installer, and
// judges the result (#211). events are the server's connections and
// disconnections since before the command.
//
//   - updated: it is connected with the new version, in the same session, for
//     settle. The installer on the server keeps watching the new agent for a
//     while after it connects, and can still roll it back.
//   - failed: it connected with the new version and then came back with
//     another one (the installer rolled it back); or, when returnTimeout ends,
//     it is not connected with the new version.
//   - a connection with the old version alone proves nothing until then: a
//     network blip can reconnect the old agent before the installer even ran.
func (h *Hub) awaitReturn(ctx context.Context, run *rolloutRun, before AgentInfo, by string, events <-chan connEvent, fail func(string) bool) bool {
	deadline := time.NewTimer(h.rollout.returnTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	timedOut := false
	var newSession time.Time // ConnectedAt of the session with the new version
	var newSince time.Time   // when that session was first seen
	sawNew := false
	lastOther := "" // the version of the last session since the command that was not the new one

	// see takes in what is known of the server: a connection or the registry
	// (connected), or a disconnection. It returns a message when the server
	// came back rolled back.
	see := func(a AgentInfo, connected bool) string {
		switch {
		case !connected:
			if a.ConnectedAt.Equal(newSession) {
				newSession = time.Time{}
			}
		case a.ConnectedAt.Equal(before.ConnectedAt):
			// still the session from before the update
		case a.Version == h.version:
			if !a.ConnectedAt.Equal(newSession) {
				newSession, newSince, sawNew = a.ConnectedAt, time.Now(), true
			}
		default:
			newSession = time.Time{}
			if sawNew {
				return fmt.Sprintf("the server connected with version %s, then came back with version %s: the installer rolled the update back", h.version, a.Version)
			}
			lastOther = a.Version
		}
		return ""
	}
	for {
		select {
		case <-ctx.Done():
			// The server has the update; what it makes of it is not known.
			msg := "the update of the servers was stopped before this server's result was known"
			h.setAgent(run, before.ID, AgentUnknown, msg)
			h.log.Warn("server result unknown", "agent", before.ID, "why", msg)
			return false
		case ev := <-events:
			if msg := see(ev.Info, ev.Connected); msg != "" {
				return fail(msg)
			}
			continue
		case <-deadline.C:
			timedOut = true
		case <-tick.C:
		}
		// The events still queued come first: they are older than what the
		// registry shows now, and taken after it they would read as a return
		// to an older version.
	drain:
		for {
			select {
			case ev := <-events:
				if msg := see(ev.Info, ev.Connected); msg != "" {
					return fail(msg)
				}
			default:
				break drain
			}
		}
		// The registry, in case an event was dropped.
		now, ok := h.agentInfo(before.ID)
		if !ok {
			newSession = time.Time{}
		} else if msg := see(now, true); msg != "" {
			return fail(msg)
		}
		if !newSession.IsZero() && ok && now.ConnectedAt.Equal(newSession) && time.Since(newSince) >= h.rollout.settle {
			h.setAgent(run, before.ID, AgentUpdated, "")
			h.audit(context.Background(), by, EventAgentUpdated, before.ID, fmt.Sprintf("%s to %s", before.Version, h.version))
			return true
		}
		// After returnTimeout only a session with the new version that is
		// still settling is waited for.
		if timedOut && newSession.IsZero() {
			switch {
			case lastOther != "":
				return fail(fmt.Sprintf("the server came back with version %s and not %s within %s: the update did not take or was rolled back", lastOther, h.version, h.rollout.returnTimeout))
			case sawNew:
				return fail(fmt.Sprintf("the server connected with version %s but did not stay connected", h.version))
			default:
				return fail(fmt.Sprintf("the server did not connect again within %s", h.rollout.returnTimeout))
			}
		}
	}
}

func (h *Hub) agentInfo(id string) (AgentInfo, bool) {
	for _, a := range h.agents.agents() {
		if a.ID == id {
			return a, true
		}
	}
	return AgentInfo{}, false
}

// autoRollout updates the servers by itself when the update mode is automatic
// and the maintenance window is open. A rollout that failed is not repeated
// until an administrator starts one.
func (h *Hub) autoRollout(ctx context.Context) {
	set, err := h.updates.Settings(ctx)
	if err != nil || set.Mode != update.ModeAutomatic || !set.InWindow(time.Now()) {
		return
	}
	h.rollout.mu.Lock()
	skip := h.rolloutRunning() || h.rollout.failedTarget == h.version
	h.rollout.mu.Unlock()
	if skip || len(h.candidates()) == 0 {
		return
	}
	if _, err := h.StartRollout(ctx, update.SystemActor, RolloutOptions{WaveSize: 1}); err != nil &&
		!errors.Is(err, ErrRolloutRunning) && !errors.Is(err, ErrNothingToRoll) {
		h.log.Warn("updating the servers automatically", "err", err)
		h.setFailedTarget(h.version)
	}
}

// FetchUpdate serves a file of the release this hub runs to an agent that was
// offered it. The agent gets its own platform's archive and nothing else.
func (s *agentServer) FetchUpdate(req *agentv1.FetchUpdateRequest, stream grpc.ServerStreamingServer[agentv1.FileChunk]) error {
	who, ok := identityFrom(stream.Context())
	if !ok || s.files == nil {
		return status.Error(codes.PermissionDenied, "only enrolled agents may fetch updates")
	}
	if req.GetVersion() != s.hubVersion {
		return status.Errorf(codes.NotFound, "this hub serves only version %s", s.hubVersion)
	}
	s.mu.Lock()
	sess := s.sessions[who.AgentID]
	s.mu.Unlock()
	if sess == nil {
		return status.Error(codes.FailedPrecondition, "the agent is not connected")
	}
	var name string
	switch req.GetFile() {
	case agentv1.UpdateFile_UPDATE_FILE_CHECKSUMS:
		name = update.FileChecksums
	case agentv1.UpdateFile_UPDATE_FILE_BUNDLE:
		name = update.FileBundle
	case agentv1.UpdateFile_UPDATE_FILE_ARCHIVE:
		v, err := update.ParseVersion(req.GetVersion())
		if err != nil {
			return status.Error(codes.InvalidArgument, "invalid version")
		}
		s.mu.Lock()
		sys := sess.info.System
		s.mu.Unlock()
		name = update.ArchiveName(v, sys.GetOs(), sys.GetArch())
	default:
		return status.Error(codes.InvalidArgument, "unknown file")
	}
	f, _, err := s.files.Open(req.GetVersion(), name)
	if err != nil {
		s.log.Warn("cannot serve an update file", "agent", who.AgentID, "file", name, "err", err)
		return status.Error(codes.NotFound, "the hub does not have this file")
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if serr := stream.Send(&agentv1.FileChunk{Data: buf[:n]}); serr != nil {
				return serr
			}
		}
		if errors.Is(err, io.EOF) {
			s.log.Info("served an update file", "agent", who.AgentID, "file", strings.ToLower(name))
			return nil
		}
		if err != nil {
			return status.Error(codes.Internal, "cannot read the file")
		}
	}
}

// stop ends a rollout in progress and waits for it.
func (r *rolloutState) stop() {
	r.mu.Lock()
	run := r.run
	if run != nil && run.status.State == RolloutRunning {
		run.status.State = RolloutStopped
		run.cancel()
	}
	r.mu.Unlock()
	if run != nil {
		<-run.done
	}
}

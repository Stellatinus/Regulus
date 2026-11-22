package hub

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func sata(device, serial string) *agentv1.PhysicalDisk {
	return &agentv1.PhysicalDisk{Device: device, Model: "ST4000VN008", Serial: serial, Transport: "sata", SizeBytes: 4 << 40}
}

func recordDisks(t *testing.T, h *Hub, agent string, at time.Time, disks ...*agentv1.PhysicalDisk) {
	t.Helper()
	if err := h.recordDisks(t.Context(), agent, disks, at); err != nil {
		t.Fatal(err)
	}
}

func TestADiskKeepsItsIdentityWhenItMovesToAnotherServer(t *testing.T) {
	h := startHub(t)
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	recordDisks(t, h, "server-a", t0, sata("sda", "ZA1"), sata("sdb", "ZA2"))
	disks, err := h.AgentDisks(t.Context(), "server-a")
	if err != nil || len(disks) != 2 {
		t.Fatalf("server-a: %v, %v", disks, err)
	}
	key := disks[0].Key
	if !disks[0].Identifiable || disks[0].Kind != "model-serial" || strings.Contains(key, "sda") {
		t.Fatalf("disk = %+v: the key must not contain the device name", disks[0])
	}
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 0 {
		t.Fatalf("moves before any move: %v", moves)
	}

	// Disk ZA1 is taken out of server-a and put in server-b, where it is sdc,
	// and it was reformatted: nothing of that changes its key.
	recordDisks(t, h, "server-a", t0.Add(time.Minute), sata("sda", "ZA2"))
	recordDisks(t, h, "server-b", t0.Add(2*time.Minute), sata("sdc", "ZA1"))

	b, _ := h.AgentDisks(t.Context(), "server-b")
	if len(b) != 1 || b[0].Key != key || b[0].Device != "sdc" || !b[0].Present {
		t.Fatalf("server-b: %+v, want the disk with key %s", b, key)
	}
	moves, err := h.DiskMoves(t.Context(), 10)
	if err != nil || len(moves) != 1 {
		t.Fatalf("moves = %v, %v", moves, err)
	}
	if m := moves[0]; m.Key != key || m.From != "server-a" || m.To != "server-b" || m.Serial != "ZA1" || !m.At.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("move = %+v", m)
	}
	// server-a no longer has it (the row moved with the disk).
	a, _ := h.AgentDisks(t.Context(), "server-a")
	if len(a) != 1 || a[0].Serial != "ZA2" {
		t.Errorf("server-a = %+v", a)
	}

	// The same report again is not a move.
	recordDisks(t, h, "server-b", t0.Add(3*time.Minute), sata("sdc", "ZA1"))
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 1 {
		t.Errorf("an unchanged report made %d moves", len(moves))
	}
}

func TestADiskTheServerStopsReportingIsKeptAsNotPresent(t *testing.T) {
	h := startHub(t)
	t0 := time.Now()
	recordDisks(t, h, "a", t0, sata("sda", "Z1"), sata("sdb", "Z2"))
	recordDisks(t, h, "a", t0.Add(time.Minute), sata("sda", "Z1"))
	disks, _ := h.AgentDisks(t.Context(), "a")
	if len(disks) != 2 || !disks[0].Present || disks[1].Present || disks[1].Serial != "Z2" {
		t.Fatalf("disks = %+v", disks)
	}
	// And coming back to the same server is not a move.
	recordDisks(t, h, "a", t0.Add(2*time.Minute), sata("sda", "Z1"), sata("sdb", "Z2"))
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 0 {
		t.Errorf("moves = %v", moves)
	}
}

func TestADiskWithoutAnIdentifierIsTrackedOnItsServerOnly(t *testing.T) {
	h := startHub(t)
	vda := &agentv1.PhysicalDisk{Device: "vda", Model: "QEMU HARDDISK", Serial: "same", Virtual: true, Transport: "virtio"}
	recordDisks(t, h, "vm1", time.Now(), vda)
	recordDisks(t, h, "vm2", time.Now(), vda)

	for _, agent := range []string{"vm1", "vm2"} {
		disks, _ := h.AgentDisks(t.Context(), agent)
		if len(disks) != 1 || disks[0].Identifiable || disks[0].Kind != "none" || disks[0].Reason != "virtual" || disks[0].Key != "slot:"+agent+":vda" {
			t.Fatalf("%s: %+v", agent, disks)
		}
	}
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 0 {
		t.Errorf("a disk that cannot be identified was matched across servers: %v", moves)
	}
	// When the slot is gone so is the record.
	recordDisks(t, h, "vm1", time.Now())
	if disks, _ := h.AgentDisks(t.Context(), "vm1"); len(disks) != 0 {
		t.Errorf("vm1 = %+v", disks)
	}
}

func TestTwoDisksWithTheSameSerialAreFlagged(t *testing.T) {
	h := startHub(t)
	recordDisks(t, h, "a", time.Now(), sata("sda", "DUP"), sata("sdb", "DUP"), sata("sdc", "OK"))
	disks, _ := h.AgentDisks(t.Context(), "a")
	if len(disks) != 3 {
		t.Fatalf("disks = %+v", disks)
	}
	for _, d := range disks {
		switch d.Serial {
		case "DUP":
			if !d.Conflict || d.Identifiable || d.Reason != "duplicate" || !strings.HasPrefix(d.Key, "slot:a:") {
				t.Errorf("duplicate disk = %+v", d)
			}
		case "OK":
			if d.Conflict || !d.Identifiable {
				t.Errorf("single disk = %+v", d)
			}
		}
	}
}

func TestOnlyTheMostRecentMovesAreKept(t *testing.T) {
	h := startHub(t)
	now := time.Now()
	d := sata("sda", "Z1")
	for i := 0; i < maxMoves+20; i++ {
		agent := "a"
		if i%2 == 0 {
			agent = "b"
		}
		recordDisks(t, h, agent, now, d)
	}
	moves, err := h.DiskMoves(t.Context(), 1000)
	if err != nil || len(moves) != 1000 {
		t.Fatalf("moves = %d, %v", len(moves), err)
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM disk_moves`).Scan(&n); err != nil || n > maxMoves {
		t.Errorf("%d moves stored (%v), want at most %d", n, err, maxMoves)
	}
}

func TestDisksAreOnlyShownWithInventoryRead(t *testing.T) {
	h := startHub(t)
	recordDisks(t, h, "a", time.Now(), sata("sda", "SECRET-SERIAL"))
	admin := newAPI(t, h, auth.RoleAdmin)

	rec := admin.do("GET", "/api/agents/a/disks", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SECRET-SERIAL") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Agent string `json:"agent"`
		Disks []struct {
			Key          string `json:"key"`
			Identifiable bool   `json:"identifiable"`
			Device       string `json:"device"`
		} `json:"disks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Agent != "a" || len(body.Disks) != 1 || !body.Disks[0].Identifiable || body.Disks[0].Device != "sda" {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	if rec := admin.do("GET", "/api/agents/nobody/disks", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disks":[]`) {
		t.Errorf("unknown agent: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do("GET", "/api/disks/moves?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("limit 0: %d", rec.Code)
	}
	if rec := admin.do("GET", "/api/disks/moves", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"moves":[]`) {
		t.Errorf("moves: %d %s", rec.Code, rec.Body)
	}
}

func TestAViewerDoesNotSeeSerialNumbers(t *testing.T) {
	h := startHub(t)
	recordDisks(t, h, "a", time.Now(), sata("sda", "SECRET-SERIAL"))
	viewer := newAPI(t, h, auth.RoleViewer)
	for _, path := range []string{"/api/agents/a/disks", "/api/disks/moves"} {
		rec := viewer.do("GET", path, nil)
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "SECRET-SERIAL") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// The serial number is not in the metrics a viewer can read either.
	if rec := viewer.do("GET", "/api/agents/a/metrics", nil); strings.Contains(rec.Body.String(), "SECRET-SERIAL") {
		t.Errorf("metrics: %s", rec.Body)
	}
}

// A report with disks is cut to a limit and its texts are cleaned, and the
// physical disks do not stay in the metrics history.
func TestPhysicalDisksAreSanitizedAndNotKeptInTheHistory(t *testing.T) {
	r := &agentv1.MetricsReport{DiskIdentityVersion: 1}
	for i := 0; i < maxDisksPerAgent+5; i++ {
		r.PhysicalDisks = append(r.PhysicalDisks, &agentv1.PhysicalDisk{Device: "sd\x00a", Serial: strings.Repeat("S", maxTextBytes+10)})
	}
	if !sanitizeReport(r) {
		t.Error("an oversized list was not reported as cut")
	}
	if len(r.PhysicalDisks) != maxDisksPerAgent || strings.ContainsRune(r.PhysicalDisks[0].Device, 0) || len(r.PhysicalDisks[0].Serial) > maxTextBytes {
		t.Errorf("not sanitized: %d disks, %q", len(r.PhysicalDisks), r.PhysicalDisks[0].Device)
	}
}

// A report that carries physical disks over the stream ends up in the disks
// table, and the disks are not part of the report kept in the history.
func TestPhysicalDisksOfAReportAreStoredAndLeftOutOfTheHistory(t *testing.T) {
	h := startHub(t)
	stream, _ := register(t, h, "a")
	report := func() *agentv1.AgentMessage {
		return &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: &agentv1.MetricsReport{
			CollectedAt:         timestamppb.Now(),
			Cpu:                 &agentv1.CpuMetrics{UsagePercent: 1},
			DiskIdentityVersion: 1,
			PhysicalDisks:       []*agentv1.PhysicalDisk{sata("sda", "STREAM1")},
		}}}
	}
	if err := stream.Send(report()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the disk is stored", func() bool {
		d, err := h.AgentDisks(t.Context(), "a")
		return err == nil && len(d) == 1 && d[0].Serial == "STREAM1"
	})
	samples, _ := h.Metrics("a")
	if len(samples) == 0 {
		t.Fatal("no metrics kept")
	}
	for _, s := range samples {
		if len(s.Report.GetPhysicalDisks()) != 0 || s.Report.GetDiskIdentityVersion() != 0 {
			t.Errorf("the history keeps the physical disks: %v", s.Report)
		}
	}
	if last := h.Agents()[0].LastMetrics; len(last.GetPhysicalDisks()) != 0 {
		t.Errorf("the agent's last report keeps the physical disks: %v", last)
	}
}

// connectFake gives the agent a session without a stream (the disks code only
// asks whether it is connected).
func connectFake(t *testing.T, h *Hub, id string) *session {
	t.Helper()
	return h.agents.register(id, &agentv1.Hello{}, 1, "", false)
}

func TestTheSameDiskFromTwoConnectedServersIsAConflictNotMoves(t *testing.T) {
	h := startHub(t)
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	a, b := connectFake(t, h, "server-a"), connectFake(t, h, "server-b")

	recordDisks(t, h, "server-a", t0, sata("sda", "CLONE1"))
	// server-b reports the same disk, over and over, while server-a does too.
	for i := 1; i <= 3; i++ {
		recordDisks(t, h, "server-b", t0.Add(time.Duration(i)*time.Minute), sata("sdx", "CLONE1"))
		recordDisks(t, h, "server-a", t0.Add(time.Duration(i)*time.Minute), sata("sda", "CLONE1"), sata("sdb", "OWN-A"))
	}
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 0 {
		t.Fatalf("a disk reported by two connected servers made %d moves: %+v", len(moves), moves)
	}
	for _, id := range []string{"server-a", "server-b"} {
		disks, err := h.AgentDisks(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		var found *DiskRecord
		for i := range disks {
			if strings.Contains(disks[i].Key, "clone1") {
				found = &disks[i]
			}
		}
		if found == nil || !found.Contested || found.Agent != "server-a" || found.ContestedBy != "server-b" {
			t.Errorf("%s sees %+v, want the disk held by server-a and contested by server-b", id, disks)
		}
	}
	if disks, _ := h.AgentDisks(t.Context(), "server-a"); len(disks) != 2 {
		t.Errorf("server-a lists %d disks, want its two", len(disks))
	}

	// server-a stops reporting it: server-b has it, and is asked to report
	// again, which records the move.
	b.diskRecheck.Store(false)
	recordDisks(t, h, "server-a", t0.Add(10*time.Minute), sata("sdb", "OWN-A"))
	if !b.diskRecheck.Load() {
		t.Fatal("server-b was not asked to report its disks again")
	}
	recordDisks(t, h, "server-b", t0.Add(11*time.Minute), sata("sdx", "CLONE1"))
	moves, _ := h.DiskMoves(t.Context(), 10)
	if len(moves) != 1 || moves[0].From != "server-a" || moves[0].To != "server-b" {
		t.Fatalf("moves = %+v, want one from server-a to server-b", moves)
	}
	disks, _ := h.AgentDisks(t.Context(), "server-b")
	if len(disks) != 1 || disks[0].Contested || disks[0].Agent != "server-b" || !disks[0].Present {
		t.Errorf("server-b = %+v", disks)
	}
	_ = a
}

func TestAConflictEndsWhenTheOtherServerStopsReportingOrGoesAway(t *testing.T) {
	h := startHub(t)
	t0 := time.Now()
	connectFake(t, h, "a")
	b := connectFake(t, h, "b")

	// b stops reporting it: no conflict, no move.
	recordDisks(t, h, "a", t0, sata("sda", "Z1"))
	recordDisks(t, h, "b", t0, sata("sdc", "Z1"))
	if d, _ := h.AgentDisks(t.Context(), "a"); len(d) != 1 || !d[0].Contested {
		t.Fatalf("not contested: %+v", d)
	}
	recordDisks(t, h, "b", t0.Add(time.Minute))
	if d, _ := h.AgentDisks(t.Context(), "a"); len(d) != 1 || d[0].Contested || d[0].ContestedBy != "" {
		t.Errorf("still contested after b stopped reporting it: %+v", d)
	}
	if moves, _ := h.DiskMoves(t.Context(), 10); len(moves) != 0 {
		t.Errorf("moves = %+v", moves)
	}

	// b contests again, then a goes away: b is asked to report again, and
	// then the disk is b's by a move.
	recordDisks(t, h, "b", t0.Add(2*time.Minute), sata("sdc", "Z1"))
	b.diskRecheck.Store(false)
	h.agents.unregister(h.agents.sessions["a"])
	waitFor(t, "b asked to report its disks again", func() bool { return b.diskRecheck.Load() })
	recordDisks(t, h, "b", t0.Add(3*time.Minute), sata("sdc", "Z1"))
	moves, _ := h.DiskMoves(t.Context(), 10)
	if len(moves) != 1 || moves[0].From != "a" || moves[0].To != "b" {
		t.Fatalf("moves = %+v, want one from a to b", moves)
	}
}

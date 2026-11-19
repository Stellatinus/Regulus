package protocol_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/CodexCircle/Regulus/internal/gen/regulus/agent/v1"
)

// -update-golden writes the golden files that do not exist yet. It never
// overwrites one: a golden file is the bytes an agent or hub of that protocol
// version really sends, and today's code must keep reading them.
var updateGolden = flag.Bool("update-golden", false, "create missing golden files in testdata (never overwrites)")

// goldenV1 are messages as protocol 1 encodes them. The expected value is
// compared field by field after decoding the stored bytes with today's code:
// a renumbered or retyped field makes the decoded message differ.
func goldenV1() map[string]proto.Message {
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	return map[string]proto.Message{
		"hello": &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Hello{Hello: &agentv1.Hello{
			ProtocolVersion: 1, MinProtocolVersion: 1, AgentVersion: "0.2.0", AgentId: "agent-0123456789abcdef",
			System: &agentv1.SystemInfo{Hostname: "web1", Os: "linux", OsVersion: "Ubuntu 26.04 LTS", Kernel: "6.17.0", Arch: "amd64", CpuCount: 4, MemoryTotalBytes: 8 << 30},
		}}},
		"welcome": &agentv1.HubMessage{Payload: &agentv1.HubMessage_Welcome{Welcome: &agentv1.Welcome{
			ProtocolVersion: 1, HubVersion: "0.2.0", AgentId: "agent-0123456789abcdef",
			HeartbeatInterval: durationpb.New(15 * time.Second), MetricsInterval: durationpb.New(10 * time.Second),
		}}},
		"reject": &agentv1.HubMessage{Payload: &agentv1.HubMessage_Reject{Reject: &agentv1.Reject{
			Reason: agentv1.Reject_REASON_AGENT_TOO_OLD, Message: "too old", MinProtocolVersion: 1, MaxProtocolVersion: 1,
		}}},
		"heartbeat": &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.New(at)}}},
		"metrics": &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_Metrics{Metrics: &agentv1.MetricsReport{
			CollectedAt:   timestamppb.New(at),
			Cpu:           &agentv1.CpuMetrics{UsagePercent: 12.5, Load_1: 0.5, Load_5: 0.25, Load_15: 0.125},
			Memory:        &agentv1.MemoryMetrics{TotalBytes: 8 << 30, UsedBytes: 2 << 30, AvailableBytes: 6 << 30, SwapTotalBytes: 1 << 30, SwapUsedBytes: 1 << 20},
			Disks:         []*agentv1.DiskMetrics{{MountPoint: "/", Filesystem: "ext4", TotalBytes: 100 << 30, UsedBytes: 40 << 30}},
			Networks:      []*agentv1.NetworkMetrics{{Interface: "eth0", RxBytes: 12345, TxBytes: 6789}},
			UptimeSeconds: 86400,
		}}},
		"command_apply_update": &agentv1.HubMessage{Payload: &agentv1.HubMessage_Command{Command: &agentv1.Command{
			Id: "cmd-1", Kind: &agentv1.Command_ApplyUpdate{ApplyUpdate: &agentv1.ApplyUpdate{
				Version: "0.3.0", Archive: "regulus_0.3.0_linux_amd64.tar.gz", Sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 31 << 20,
			}},
		}}},
		"command_ping": &agentv1.HubMessage{Payload: &agentv1.HubMessage_Command{Command: &agentv1.Command{Id: "cmd-2", Kind: &agentv1.Command_Ping{Ping: &agentv1.Ping{}}}}},
		"command_result": &agentv1.AgentMessage{Payload: &agentv1.AgentMessage_CommandResult{CommandResult: &agentv1.CommandResult{
			CommandId: "cmd-1", Success: false, Error: "an update is already being fetched",
		}}},
		"fetch_update_request": &agentv1.FetchUpdateRequest{Version: "0.3.0", File: agentv1.UpdateFile_UPDATE_FILE_ARCHIVE},
	}
}

// TestGoldenMessagesOfProtocol1 decodes bytes written by protocol 1 with the
// code of today. Hubs and agents of different releases talk to each other
// (docs/versioning.md); this fails when a change to the .proto files would
// make one of them misread the other. The update contract (hello, welcome,
// heartbeat, the commands, fetch_update_request) must never fail here.
func TestGoldenMessagesOfProtocol1(t *testing.T) {
	dir := filepath.Join("testdata", "v1")
	for name, want := range goldenV1() {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".binpb")
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) && *updateGolden {
				enc, merr := proto.MarshalOptions{Deterministic: true}.Marshal(want)
				if merr != nil {
					t.Fatal(merr)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, enc, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("created %s", path)
				return
			}
			if err != nil {
				t.Fatalf("%v (create it once with -update-golden)", err)
			}
			got := want.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(data, got); err != nil {
				t.Fatalf("today's code cannot decode a protocol 1 %s: %v", name, err)
			}
			if !proto.Equal(got, want) {
				t.Errorf("a protocol 1 %s decodes to\n%v\nwant\n%v", name, got, want)
			}
			if len(got.ProtoReflect().GetUnknown()) != 0 {
				t.Errorf("a protocol 1 %s has fields today's code does not know: a field was removed or renumbered", name)
			}
		})
	}
}

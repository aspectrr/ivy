package ivyv1

import (
	"testing"
)

func TestRegistrationMessage(t *testing.T) {
	reg := &Registration{
		HostId:             "parser-host-01",
		Hostname:           "logstash-prod-01",
		AllowedDirectories: []string{"/etc/logstash", "/var/log/logstash"},
	}

	if reg.HostId != "parser-host-01" {
		t.Errorf("HostId = %q, want parser-host-01", reg.HostId)
	}
	if reg.Hostname != "logstash-prod-01" {
		t.Errorf("Hostname = %q, want logstash-prod-01", reg.Hostname)
	}
	if len(reg.AllowedDirectories) != 2 {
		t.Errorf("AllowedDirectories len = %d, want 2", len(reg.AllowedDirectories))
	}
}

func TestExecuteCommandRequest(t *testing.T) {
	req := &ExecuteCommandRequest{
		RequestId:      "req-123",
		Command:        CommandType_GREP,
		Args:           []string{"-r", "error", "/etc/logstash"},
		WorkingDir:     "/etc/logstash",
		TimeoutSeconds: 30,
	}

	if req.RequestId != "req-123" {
		t.Errorf("RequestId = %q, want req-123", req.RequestId)
	}
	if req.Command != CommandType_GREP {
		t.Errorf("Command = %v, want GREP", req.Command)
	}
	if len(req.Args) != 3 {
		t.Fatalf("Args len = %d, want 3", len(req.Args))
	}
	if req.Args[0] != "-r" {
		t.Errorf("Args[0] = %q, want -r", req.Args[0])
	}
	if req.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %d, want 30", req.TimeoutSeconds)
	}
}

func TestExecuteCommandResponse(t *testing.T) {
	resp := &ExecuteCommandResponse{
		RequestId: "req-123",
		ExitCode:  0,
		Stdout:    "file.conf:10: error pattern found",
		Stderr:    "",
		Timeout:   false,
	}

	if resp.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", resp.ExitCode)
	}
	if resp.Timeout {
		t.Error("Timeout = true, want false")
	}
}

func TestCommandTypeEnum(t *testing.T) {
	expected := map[CommandType]string{
		CommandType_COMMAND_TYPE_UNSPECIFIED: "COMMAND_TYPE_UNSPECIFIED",
		CommandType_GREP:                     "GREP",
		CommandType_AWK:                      "AWK",
		CommandType_FIND:                     "FIND",
		CommandType_CAT:                      "CAT",
		CommandType_READ_FILE:                "READ_FILE",
		CommandType_TAIL:                     "TAIL",
		CommandType_SYSTEMCTL_STATUS:         "SYSTEMCTL_STATUS",
		CommandType_JOURNALCTL:               "JOURNALCTL",
	}

	for cmd, name := range expected {
		if cmd.String() != name {
			t.Errorf("CommandType(%d).String() = %q, want %q", cmd, cmd.String(), name)
		}
	}
}

func TestLeafMessage_CommandOutput(t *testing.T) {
	msg := &LeafMessage{
		Payload: &LeafMessage_CommandOutput{
			CommandOutput: &CommandOutput{
				RequestId: "req-456",
				ExitCode:  1,
				Stdout:    "",
				Stderr:    "permission denied",
				Timeout:   false,
			},
		},
	}

	output := msg.GetCommandOutput()
	if output == nil {
		t.Fatal("GetCommandOutput() = nil, want non-nil")
	}
	if output.RequestId != "req-456" {
		t.Errorf("RequestId = %q, want req-456", output.RequestId)
	}
	if output.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", output.ExitCode)
	}
}

func TestLeafMessage_Heartbeat(t *testing.T) {
	msg := &LeafMessage{
		Payload: &LeafMessage_Heartbeat{
			Heartbeat: &Heartbeat{
				TimestampMs: 1234567890,
			},
		},
	}

	hb := msg.GetHeartbeat()
	if hb == nil {
		t.Fatal("GetHeartbeat() = nil, want non-nil")
	}
	if hb.TimestampMs != 1234567890 {
		t.Errorf("TimestampMs = %d, want 1234567890", hb.TimestampMs)
	}
}

func TestVineCommand_ExecuteCommand(t *testing.T) {
	cmd := &VineCommand{
		Payload: &VineCommand_ExecuteCommand{
			ExecuteCommand: &ExecuteCommandRequest{
				RequestId: "req-789",
				Command:   CommandType_FIND,
				Args:      []string{"/etc/logstash", "-name", "*.conf"},
			},
		},
	}

	req := cmd.GetExecuteCommand()
	if req == nil {
		t.Fatal("GetExecuteCommand() = nil, want non-nil")
	}
	if req.Command != CommandType_FIND {
		t.Errorf("Command = %v, want FIND", req.Command)
	}
}

func TestSyncDirectoryRequest(t *testing.T) {
	req := &SyncDirectoryRequest{
		RequestId: "sync-001",
		Directory: "/etc/logstash",
	}

	if req.Directory != "/etc/logstash" {
		t.Errorf("Directory = %q, want /etc/logstash", req.Directory)
	}
}

func TestFileInfo(t *testing.T) {
	fi := &FileInfo{
		Path:           "/etc/logstash/conf.d/01-input.conf",
		Size:           1024,
		ChecksumSha256: "abc123def456",
	}

	if fi.Size != 1024 {
		t.Errorf("Size = %d, want 1024", fi.Size)
	}
}

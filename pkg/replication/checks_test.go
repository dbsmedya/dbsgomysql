package replication

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// gtidModeOffValue is the server's spelling for a disabled GTID mode. It is a
// constant so that this file does not repeat the literal, which would push the
// package-wide count past goconst's threshold and report against decode.go.
const gtidModeOffValue = "OFF"

func runningChannel(name string) ChannelStatus {
	return ChannelStatus{
		ChannelName:         name,
		IORunning:           "Yes",
		SQLRunning:          "Yes",
		SecondsBehindSource: sql.NullInt64{Int64: 0, Valid: true},
		SourceHost:          "db1",
		SourcePort:          3306,
	}
}

func laggingChannel(name string, seconds sql.NullInt64) ChannelStatus {
	channel := runningChannel(name)
	channel.SecondsBehindSource = seconds

	return channel
}

func assertFindingShape(t *testing.T, finding Finding, id string, channels []string) {
	t.Helper()

	if finding.Check != id {
		t.Errorf("Finding.Check = %q, want %q", finding.Check, id)
	}
	if len(finding.Channels) != len(channels) {
		t.Fatalf("Finding.Channels = %q, want %q", finding.Channels, channels)
	}
	for index, name := range channels {
		if finding.Channels[index] != name {
			t.Errorf("Finding.Channels[%d] = %q, want %q", index, finding.Channels[index], name)
		}
	}

	info, ok := LookupCheck(id)
	if !ok {
		t.Fatalf("LookupCheck(%q) found = false, want true", id)
	}
	if !strings.HasSuffix(finding.Message, info.Rationale) {
		t.Errorf("Finding.Message = %q, want it to end with the catalog rationale %q",
			finding.Message, info.Rationale)
	}
	if !strings.Contains(finding.Message, ". ") {
		t.Errorf("Finding.Message = %q, want a summary and rationale joined by %q",
			finding.Message, ". ")
	}
}

func TestCheckBinaryLogEnabled(t *testing.T) {
	t.Parallel()

	if findings := CheckBinaryLogEnabled(true); len(findings) != 0 {
		t.Errorf("CheckBinaryLogEnabled(true) = %#v, want no findings", findings)
	}

	findings := CheckBinaryLogEnabled(false)
	if len(findings) != 1 {
		t.Fatalf("CheckBinaryLogEnabled(false) returned %d findings, want 1", len(findings))
	}
	assertFindingShape(t, findings[0], IDBinaryLogEnabled, nil)

	facts, ok := findings[0].Facts.(bool)
	if !ok {
		t.Fatalf("Finding.Facts is %T, want bool", findings[0].Facts)
	}
	if facts {
		t.Error("Finding.Facts = true, want the false that caused the finding")
	}
}

func TestCheckGTIDModeOn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		mode        string
		wantFinding bool
	}{
		{name: "on", mode: "ON", wantFinding: false},
		{name: "off", mode: gtidModeOffValue, wantFinding: true},
		{name: "off permissive", mode: "OFF_PERMISSIVE", wantFinding: true},
		{name: "on permissive", mode: "ON_PERMISSIVE", wantFinding: true},
		{name: "unrecognized", mode: "Banana", wantFinding: true},
		{name: "lower case is not folded", mode: "on", wantFinding: true},
		{name: "empty", mode: "", wantFinding: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			status := GTIDStatus{Mode: testCase.mode, Executed: "uuid:1-5"}
			findings := CheckGTIDModeOn(status)

			if !testCase.wantFinding {
				if len(findings) != 0 {
					t.Fatalf("CheckGTIDModeOn(%q) = %#v, want no findings", testCase.mode, findings)
				}

				return
			}

			if len(findings) != 1 {
				t.Fatalf("CheckGTIDModeOn(%q) returned %d findings, want 1",
					testCase.mode, len(findings))
			}
			assertFindingShape(t, findings[0], IDGTIDModeOn, nil)

			facts, ok := findings[0].Facts.(GTIDStatus)
			if !ok {
				t.Fatalf("Finding.Facts is %T, want GTIDStatus", findings[0].Facts)
			}
			if facts != status {
				t.Errorf("Finding.Facts = %#v, want %#v", facts, status)
			}
		})
	}
}

func TestCheckReplicationConfigured(t *testing.T) {
	t.Parallel()

	t.Run("no channels", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{}
		findings := CheckReplicationConfigured(channels)
		if len(findings) != 1 {
			t.Fatalf("CheckReplicationConfigured(empty) returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationConfigured, nil)

		facts, ok := findings[0].Facts.([]ChannelStatus)
		if !ok {
			t.Fatalf("Finding.Facts is %T, want []ChannelStatus", findings[0].Facts)
		}
		if len(facts) != 0 {
			t.Errorf("Finding.Facts = %#v, want the empty channel slice", facts)
		}
	})

	t.Run("nil channels", func(t *testing.T) {
		t.Parallel()

		if findings := CheckReplicationConfigured(nil); len(findings) != 1 {
			t.Fatalf("CheckReplicationConfigured(nil) returned %d findings, want 1", len(findings))
		}
	})

	t.Run("one channel", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{runningChannel("ch1")}
		if findings := CheckReplicationConfigured(channels); len(findings) != 0 {
			t.Errorf("CheckReplicationConfigured(one channel) = %#v, want no findings", findings)
		}
	})
}

func TestCheckReplicationChannelsRunning(t *testing.T) {
	t.Parallel()

	t.Run("running channel passes", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{runningChannel("ch1")}
		if findings := CheckReplicationChannelsRunning(channels); len(findings) != 0 {
			t.Errorf("CheckReplicationChannelsRunning(running) = %#v, want no findings", findings)
		}
	})

	t.Run("empty slice yields nothing", func(t *testing.T) {
		t.Parallel()

		if findings := CheckReplicationChannelsRunning([]ChannelStatus{}); len(findings) != 0 {
			t.Errorf("CheckReplicationChannelsRunning(empty) = %#v, want no findings", findings)
		}
	})

	t.Run("connecting fails", func(t *testing.T) {
		t.Parallel()

		channel := runningChannel("ch1")
		channel.IORunning = "Connecting"

		findings := CheckReplicationChannelsRunning([]ChannelStatus{channel})
		if len(findings) != 1 {
			t.Fatalf("returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationChannelsRunning, []string{"ch1"})
	})

	t.Run("unrecognized value fails closed", func(t *testing.T) {
		t.Parallel()

		channel := runningChannel("ch1")
		channel.IORunning = "Banana"

		findings := CheckReplicationChannelsRunning([]ChannelStatus{channel})
		if len(findings) != 1 {
			t.Fatalf("returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationChannelsRunning, []string{"ch1"})
	})

	t.Run("deliberate stop carries empty last errors", func(t *testing.T) {
		t.Parallel()

		// A deliberate STOP REPLICA records no error: errno 0 and an empty
		// message are the server's documented no-error state, so the finding
		// must still fire and must carry those empty values as they are.
		channel := runningChannel("ch1")
		channel.SQLRunning = "No"
		channel.LastSQLErrno = 0
		channel.LastSQLError = ""

		findings := CheckReplicationChannelsRunning([]ChannelStatus{channel})
		if len(findings) != 1 {
			t.Fatalf("returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationChannelsRunning, []string{"ch1"})

		facts, ok := findings[0].Facts.(ChannelStatus)
		if !ok {
			t.Fatalf("Finding.Facts is %T, want ChannelStatus", findings[0].Facts)
		}
		if facts != channel {
			t.Errorf("Finding.Facts = %#v, want %#v", facts, channel)
		}
		if facts.LastSQLErrno != 0 || facts.LastSQLError != "" {
			t.Errorf("Finding.Facts last SQL error = (%d, %q), want the empty no-error state",
				facts.LastSQLErrno, facts.LastSQLError)
		}
	})

	t.Run("ordering preserved across channels", func(t *testing.T) {
		t.Parallel()

		first := runningChannel("ch1")
		first.SQLRunning = "No"
		second := runningChannel("ch2")
		third := runningChannel("ch3")
		third.IORunning = "Connecting"

		findings := CheckReplicationChannelsRunning([]ChannelStatus{first, second, third})
		if len(findings) != 2 {
			t.Fatalf("returned %d findings, want 2", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationChannelsRunning, []string{"ch1"})
		assertFindingShape(t, findings[1], IDReplicationChannelsRunning, []string{"ch3"})
	})

	t.Run("default channel keeps its empty name", func(t *testing.T) {
		t.Parallel()

		channel := runningChannel("")
		channel.SQLRunning = "No"

		findings := CheckReplicationChannelsRunning([]ChannelStatus{channel})
		if len(findings) != 1 {
			t.Fatalf("returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDReplicationChannelsRunning, []string{""})
	})
}

func TestCheckSecondsBehindSourceWithin(t *testing.T) {
	t.Parallel()

	t.Run("equal to the bound passes", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{laggingChannel("ch1", sql.NullInt64{Int64: 5, Valid: true})}
		if findings := CheckSecondsBehindSourceWithin(channels, 5); len(findings) != 0 {
			t.Errorf("lag 5 against max 5 = %#v, want no findings", findings)
		}
	})

	t.Run("above the bound fails", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{laggingChannel("ch1", sql.NullInt64{Int64: 6, Valid: true})}
		findings := CheckSecondsBehindSourceWithin(channels, 5)
		if len(findings) != 1 {
			t.Fatalf("lag 6 against max 5 returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDSecondsBehindSourceWithin, []string{"ch1"})

		facts, ok := findings[0].Facts.(ChannelStatus)
		if !ok {
			t.Fatalf("Finding.Facts is %T, want ChannelStatus", findings[0].Facts)
		}
		if facts.SecondsBehindSource.Int64 != 6 {
			t.Errorf("Finding.Facts lag = %#v, want 6", facts.SecondsBehindSource)
		}
	})

	t.Run("NULL fails closed", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{laggingChannel("ch1", sql.NullInt64{})}
		findings := CheckSecondsBehindSourceWithin(channels, 3600)
		if len(findings) != 1 {
			t.Fatalf("NULL lag returned %d findings, want 1", len(findings))
		}
		assertFindingShape(t, findings[0], IDSecondsBehindSourceWithin, []string{"ch1"})
	})

	t.Run("negative bound fails every channel", func(t *testing.T) {
		t.Parallel()

		// ch2 carries a lag below the bound itself: a constructed value a
		// consumer can supply, and the one that a plain "lag above the bound"
		// comparison lets through. Every supplied channel fails a negative
		// bound, so this one fails too.
		channels := []ChannelStatus{
			laggingChannel("ch1", sql.NullInt64{Int64: 0, Valid: true}),
			laggingChannel("ch2", sql.NullInt64{Int64: -2, Valid: true}),
		}

		findings := CheckSecondsBehindSourceWithin(channels, -1)
		if len(findings) != 2 {
			t.Fatalf("max -1 with two running channels returned %d findings, want 2", len(findings))
		}
		assertFindingShape(t, findings[0], IDSecondsBehindSourceWithin, []string{"ch1"})
		assertFindingShape(t, findings[1], IDSecondsBehindSourceWithin, []string{"ch2"})
	})

	t.Run("empty slice yields nothing", func(t *testing.T) {
		t.Parallel()

		if findings := CheckSecondsBehindSourceWithin([]ChannelStatus{}, 5); len(findings) != 0 {
			t.Errorf("empty channels = %#v, want no findings", findings)
		}
	})

	t.Run("ordering preserved across channels", func(t *testing.T) {
		t.Parallel()

		channels := []ChannelStatus{
			laggingChannel("ch1", sql.NullInt64{Int64: 90, Valid: true}),
			laggingChannel("ch2", sql.NullInt64{Int64: 1, Valid: true}),
			laggingChannel("ch3", sql.NullInt64{}),
		}

		findings := CheckSecondsBehindSourceWithin(channels, 5)
		if len(findings) != 2 {
			t.Fatalf("returned %d findings, want 2", len(findings))
		}
		assertFindingShape(t, findings[0], IDSecondsBehindSourceWithin, []string{"ch1"})
		assertFindingShape(t, findings[1], IDSecondsBehindSourceWithin, []string{"ch3"})
	})
}

// TestJobLoopRecipeComposition pins the documented gate: an unconfigured
// server must never pass it. REPLICATION_CONFIGURED is what makes an empty
// snapshot fail, since the per-channel checks have nothing to report.
func TestJobLoopRecipeComposition(t *testing.T) {
	t.Parallel()

	gate := func(channels []ChannelStatus, maxSeconds int64) []Finding {
		findings := CheckReplicationConfigured(channels)
		findings = append(findings, CheckReplicationChannelsRunning(channels)...)
		findings = append(findings, CheckSecondsBehindSourceWithin(channels, maxSeconds)...)

		return findings
	}

	t.Run("empty snapshot fails the gate", func(t *testing.T) {
		t.Parallel()

		findings := gate([]ChannelStatus{}, 30)
		if len(findings) == 0 {
			t.Fatal("empty snapshot passed the gate; it must fail via REPLICATION_CONFIGURED")
		}
		if findings[0].Check != IDReplicationConfigured {
			t.Errorf("first finding = %q, want %q", findings[0].Check, IDReplicationConfigured)
		}
	})

	t.Run("filter matching nothing fails the gate", func(t *testing.T) {
		t.Parallel()

		snapshot := []ChannelStatus{runningChannel("ch1")}

		// The documented named-channel filter, matching no channel.
		var filtered []ChannelStatus
		for _, channel := range snapshot {
			if channel.ChannelName == "absent" {
				filtered = append(filtered, channel)
			}
		}
		if len(filtered) != 0 {
			t.Fatalf("filter matched %d channels, want 0", len(filtered))
		}

		findings := gate(filtered, 30)
		if len(findings) == 0 {
			t.Fatal("filtered-to-empty snapshot passed the gate; it must fail via REPLICATION_CONFIGURED")
		}
		if findings[0].Check != IDReplicationConfigured {
			t.Errorf("first finding = %q, want %q", findings[0].Check, IDReplicationConfigured)
		}
	})

	t.Run("healthy snapshot passes the gate", func(t *testing.T) {
		t.Parallel()

		snapshot := []ChannelStatus{
			laggingChannel("ch1", sql.NullInt64{Int64: 0, Valid: true}),
			laggingChannel("ch2", sql.NullInt64{Int64: 30, Valid: true}),
		}
		if findings := gate(snapshot, 30); len(findings) != 0 {
			t.Errorf("healthy snapshot = %#v, want an empty gate", findings)
		}
	})
}

func TestFindingJSONContract(t *testing.T) {
	t.Parallel()

	finding := Finding{
		Check:    IDGTIDModeOn,
		Message:  "GTID mode is OFF. Consumers need ON.",
		Channels: []string{"ch1"},
		Facts:    GTIDStatus{Mode: gtidModeOffValue, Executed: "uuid:1-5", Purged: ""},
	}

	const want = `{"check":"GTID_MODE_ON","message":"GTID mode is OFF. Consumers need ON.",` +
		`"channels":["ch1"],"facts":{"mode":"OFF","executed":"uuid:1-5","purged":""}}`

	encoded, err := json.Marshal(finding)
	if err != nil {
		t.Fatalf("json.Marshal returned error %v, want nil", err)
	}
	if string(encoded) != want {
		t.Fatalf("json.Marshal = %s, want %s", encoded, want)
	}

	var decoded Finding
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error %v, want nil", err)
	}
	if decoded.Check != finding.Check {
		t.Errorf("round-tripped Check = %q, want %q", decoded.Check, finding.Check)
	}
	if decoded.Message != finding.Message {
		t.Errorf("round-tripped Message = %q, want %q", decoded.Message, finding.Message)
	}
	if len(decoded.Channels) != 1 || decoded.Channels[0] != "ch1" {
		t.Errorf("round-tripped Channels = %q, want [ch1]", decoded.Channels)
	}
	if decoded.Facts == nil {
		t.Error("round-tripped Facts = nil, want the decoded payload")
	}
}

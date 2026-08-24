package sol_test

import (
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		ok      bool
		phase   string
		state   string
		percent *int
		seq     int
	}{
		{
			name: "progress",
			line: "SHOAL|1|41|2026-06-19T04:10:11Z|IMAGE_WRITE|65|OK|writing rootfs to /dev/nvme0n1",
			ok:   true, phase: "IMAGE_WRITE", state: "OK", percent: intPtr(65), seq: 41,
		},
		{
			name: "heartbeat",
			line: "SHOAL|1|42|2026-06-19T04:10:21Z|IMAGE_WRITE|-|HEARTBEAT|",
			ok:   true, phase: "IMAGE_WRITE", state: "HEARTBEAT", percent: nil, seq: 42,
		},
		{
			name: "done",
			line: "SHOAL|1|43|2026-06-19T04:11:02Z|DONE|100|OK|reboot pending",
			ok:   true, phase: "DONE", state: "OK", percent: intPtr(100), seq: 43,
		},
		{
			name: "noise prefix",
			line: "[    2.1] SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|0|OK|starting",
			ok:   true, phase: "BOOT", state: "OK", percent: intPtr(0), seq: 1,
		},
		{name: "ignore non marker", line: "login:", ok: false},
		{name: "bad version", line: "SHOAL|2|1|2026-06-19T04:10:00Z|BOOT|0|OK|x", ok: false},
		{name: "bad state", line: "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|0|NOPE|x", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ok := sol.ParseLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if m.Phase != tc.phase || m.State != tc.state || m.Seq != tc.seq {
				t.Fatalf("got phase=%s state=%s seq=%d", m.Phase, m.State, m.Seq)
			}
			if tc.percent == nil {
				if m.Percent != nil {
					t.Fatalf("percent want nil got %v", *m.Percent)
				}
			} else if m.Percent == nil || *m.Percent != *tc.percent {
				t.Fatalf("percent mismatch")
			}
			if m.Timestamp.IsZero() || m.Timestamp.Location() != time.UTC {
				// UTC after parse
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	m, ok := sol.ParseLine("SHOAL|1|1|2026-06-19T04:11:02Z|DONE|100|OK|x")
	if !ok || !sol.IsTerminal(m) {
		t.Fatal("DONE/OK should be terminal")
	}
	m, ok = sol.ParseLine("SHOAL|1|2|2026-06-19T04:11:02Z|IMAGE_WRITE|10|ERROR|disk full")
	if !ok || !sol.IsTerminal(m) {
		t.Fatal("ERROR should be terminal")
	}
	m, ok = sol.ParseLine("SHOAL|1|3|2026-06-19T04:11:02Z|IMAGE_WRITE|10|OK|x")
	if !ok || sol.IsTerminal(m) {
		t.Fatal("progress should not be terminal")
	}
}

func TestIsStageComplete(t *testing.T) {
	m, ok := sol.ParseLine("SHOAL|1|1|2026-06-19T04:11:02Z|PREP_DONE|100|OK|ready")
	if !ok || sol.IsTerminal(m) {
		t.Fatal("PREP_DONE must not be job-terminal")
	}
	if !sol.IsStageComplete(m) {
		t.Fatal("PREP_DONE must be stage-complete")
	}
	m, ok = sol.ParseLine("SHOAL|1|2|2026-06-19T04:11:02Z|DONE|100|OK|x")
	if !ok || !sol.IsStageComplete(m) {
		t.Fatal("DONE should be stage-complete")
	}
}

func TestFormatMarkerLine(t *testing.T) {
	p := 65
	m := models.SOLMarker{
		SchemaVer: 1,
		Seq:       41,
		Timestamp: mustTime(t, "2026-06-19T04:10:11Z"),
		Phase:     "IMAGE_WRITE",
		Percent:   &p,
		State:     "OK",
		Detail:    "writing rootfs",
	}
	got := sol.FormatMarkerLine(m)
	want := "SHOAL|1|41|2026-06-19T04:10:11Z|IMAGE_WRITE|65|OK|writing rootfs"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Round-trip through ParseLine.
	parsed, ok := sol.ParseLine(got)
	if !ok {
		t.Fatal("parse failed")
	}
	if parsed.Phase != "IMAGE_WRITE" || parsed.Seq != 41 || parsed.Percent == nil || *parsed.Percent != 65 {
		t.Fatalf("%+v", parsed)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func intPtr(v int) *int { return &v }

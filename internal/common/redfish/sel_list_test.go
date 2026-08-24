package redfish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gofishredfish "github.com/stmcginnis/gofish/redfish"
)

func TestSensorUnavailableNote(t *testing.T) {
	got := sensorUnavailableNote(nil, true)
	if got != "No reading while host is off" {
		t.Fatalf("%q", got)
	}
	got = sensorUnavailableNote(nil, false)
	if got != "BMC did not return a reading" {
		t.Fatalf("%q", got)
	}
}

func TestUniqueSensorName(t *testing.T) {
	seen := map[string]struct{}{}
	a := uniqueSensorName(seen, "InputPowerSensor", "PSU.Slot.1_InputPower")
	if a != "InputPowerSensor" {
		t.Fatalf("first=%q", a)
	}
	seen[strings.ToLower(a)] = struct{}{}
	b := uniqueSensorName(seen, "InputPowerSensor", "PSU.Slot.2_InputPower")
	if b != "InputPowerSensor (PSU.Slot.2_InputPower)" {
		t.Fatalf("second=%q", b)
	}
}

func TestDiscretePowerGood(t *testing.T) {
	if !discretePowerGood("CPU1 VCCIN PG") || !discretePowerGood("System Board Pfault Fail Safe") {
		t.Fatal("expected discrete PG names")
	}
	if discretePowerGood("PS1 Voltage 1") || discretePowerGood("Inlet Temp") {
		t.Fatal("analog rails should not be treated as PG bits")
	}
}

func TestLogServiceRank(t *testing.T) {
	sel := &gofishredfish.LogService{LogEntryType: gofishredfish.SELLogEntryTypes}
	sel.Name, sel.ID, sel.ODataID = "SEL Log", "Sel", "/redfish/v1/Managers/1/LogServices/Sel"

	lc := &gofishredfish.LogService{}
	lc.Name, lc.ID, lc.ODataID = "LC Log", "Lclog", "/redfish/v1/Managers/1/LogServices/Lclog"

	fault := &gofishredfish.LogService{}
	fault.Name, fault.ID, fault.ODataID = "Fault List", "FaultList", "/redfish/v1/Managers/1/LogServices/FaultList"

	if logServiceRank(sel) != logRankSEL {
		t.Fatalf("SEL rank=%d want %d", logServiceRank(sel), logRankSEL)
	}
	if logServiceRank(lc) != logRankVerbose {
		t.Fatalf("Lclog rank=%d want %d", logServiceRank(lc), logRankVerbose)
	}
	if logServiceRank(fault) != logRankVerbose {
		t.Fatalf("FaultList rank=%d want %d", logServiceRank(fault), logRankVerbose)
	}
	if logServiceRank(sel) <= logServiceRank(lc) {
		t.Fatal("SEL should outrank Lclog")
	}
}

func TestListSELSkipsLifecycleLog(t *testing.T) {
	var lcHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		if strings.Contains(p, "Lclog") && strings.Contains(p, "Entries") {
			lcHits.Add(1)
			http.Error(w, "lclog should not be fetched", http.StatusInternalServerError)
			return
		}
		body, ok := selSkipLCLogJSON(p)
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	c := openFakeClient(t, srv.URL)
	got, err := c.ListSEL(context.Background(), "1", SELOptions{MaxEntries: 10})
	if err != nil {
		t.Fatalf("ListSEL: %v", err)
	}
	if lcHits.Load() != 0 {
		t.Fatalf("Lclog Entries fetched %d times; want 0", lcHits.Load())
	}
	if len(got) != 1 || got[0].Message != "chassis closed" {
		t.Fatalf("got %+v", got)
	}
	if got[0].LogService != "SEL Log" {
		t.Fatalf("log service=%q", got[0].LogService)
	}
}

func selSkipLCLogJSON(path string) (string, bool) {
	switch path {
	case "/redfish/v1", "/redfish/v1/":
		return `{
			"@odata.id": "/redfish/v1/",
			"Id": "RootService",
			"Name": "Root Service",
			"RedfishVersion": "1.9.0",
			"Systems": {"@odata.id": "/redfish/v1/Systems"},
			"Managers": {"@odata.id": "/redfish/v1/Managers"},
			"Chassis": {"@odata.id": "/redfish/v1/Chassis"}
		}`, true
	case "/redfish/v1/Systems":
		return collectionJSON("/redfish/v1/Systems", "/redfish/v1/Systems/1"), true
	case "/redfish/v1/Systems/1":
		return `{
			"@odata.id": "/redfish/v1/Systems/1",
			"Id": "1",
			"Name": "System.1",
			"Manufacturer": "Dell Inc.",
			"Model": "PowerEdge R750",
			"PowerState": "Off"
		}`, true
	case "/redfish/v1/Managers":
		return collectionJSON("/redfish/v1/Managers", "/redfish/v1/Managers/1"), true
	case "/redfish/v1/Managers/1":
		return `{
			"@odata.id": "/redfish/v1/Managers/1",
			"Id": "1",
			"Name": "Manager",
			"LogServices": {"@odata.id": "/redfish/v1/Managers/1/LogServices"}
		}`, true
	case "/redfish/v1/Managers/1/LogServices":
		return collectionJSON("/redfish/v1/Managers/1/LogServices",
			"/redfish/v1/Managers/1/LogServices/Sel",
			"/redfish/v1/Managers/1/LogServices/Lclog"), true
	case "/redfish/v1/Managers/1/LogServices/Sel":
		return `{
			"@odata.id": "/redfish/v1/Managers/1/LogServices/Sel",
			"Id": "Sel",
			"Name": "SEL Log",
			"LogEntryType": "SEL",
			"Entries": {"@odata.id": "/redfish/v1/Managers/1/LogServices/Sel/Entries"}
		}`, true
	case "/redfish/v1/Managers/1/LogServices/Lclog":
		return `{
			"@odata.id": "/redfish/v1/Managers/1/LogServices/Lclog",
			"Id": "Lclog",
			"Name": "LC Log",
			"LogEntryType": "Event",
			"Entries": {"@odata.id": "/redfish/v1/Managers/1/LogServices/Lclog/Entries"}
		}`, true
	case "/redfish/v1/Managers/1/LogServices/Sel/Entries":
		return `{
			"@odata.id": "/redfish/v1/Managers/1/LogServices/Sel/Entries",
			"Members@odata.count": 1,
			"Members": [{"@odata.id": "/redfish/v1/Managers/1/LogServices/Sel/Entries/1"}]
		}`, true
	case "/redfish/v1/Managers/1/LogServices/Sel/Entries/1":
		return `{
			"@odata.id": "/redfish/v1/Managers/1/LogServices/Sel/Entries/1",
			"Id": "1",
			"Name": "Log Entry 1",
			"Message": "chassis closed",
			"Severity": "OK",
			"EntryType": "SEL",
			"Created": "2025-05-02T09:10:01-05:00"
		}`, true
	case "/redfish/v1/Chassis":
		return `{
			"@odata.id": "/redfish/v1/Chassis",
			"Name": "Chassis Collection",
			"Members@odata.count": 0,
			"Members": []
		}`, true
	default:
		return "", false
	}
}

func collectionJSON(self string, members ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"@odata.id":%q,"Name":"Collection","Members@odata.count":%d,"Members":[`, self, len(members))
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"@odata.id":%q}`, m)
	}
	b.WriteString(`]}`)
	return b.String()
}

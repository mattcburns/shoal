package redfish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSessionAuth exercises Config.AuthMode=="session": Open must log in via
// the Redfish SessionService (POST Links.Sessions), then use the returned
// X-Auth-Token (not Basic-Auth) on every subsequent request, and Close must
// log the session back out.
func TestSessionAuth(t *testing.T) {
	var (
		sessionPOSTs   int
		sessionDELETEs int
		sawBasicAuth   bool
		gotToken       string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/",
			"Id": "RootService",
			"Name": "Root Service",
			"RedfishVersion": "1.9.0",
			"Systems": {"@odata.id": "/redfish/v1/Systems"},
			"Managers": {"@odata.id": "/redfish/v1/Managers"},
			"Links": {"Sessions": {"@odata.id": "/redfish/v1/SessionService/Sessions"}}
		}`)
	})
	mux.HandleFunc("/redfish/v1/SessionService/Sessions", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			sawBasicAuth = true
		}
		switch r.Method {
		case http.MethodPost:
			sessionPOSTs++
			var body struct {
				UserName string
				Password string
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.UserName != "admin" || body.Password != "password" {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-Auth-Token", "tok-abc123")
			w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/1")
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/redfish/v1/SessionService/Sessions/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			sessionDELETEs++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/redfish/v1/Systems", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			sawBasicAuth = true
		}
		gotToken = r.Header.Get("X-Auth-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Systems",
			"Name": "Systems Collection",
			"Members@odata.count": 0,
			"Members": []
		}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bmc, err := NewBMC(Config{
		BaseURL: srv.URL, Username: "admin", Password: "password", AuthMode: "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := bmc.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sessionPOSTs != 1 {
		t.Fatalf("session POSTs = %d, want 1", sessionPOSTs)
	}

	if _, err := bmc.ListSystems(ctx); err != nil {
		t.Fatalf("ListSystems: %v", err)
	}
	if gotToken != "tok-abc123" {
		t.Fatalf("X-Auth-Token on request = %q, want tok-abc123", gotToken)
	}
	if sawBasicAuth {
		t.Fatal("session mode must not also send Basic-Auth")
	}

	if err := bmc.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if sessionDELETEs != 1 {
		t.Fatalf("session DELETEs = %d, want 1 (Close must log out)", sessionDELETEs)
	}
}

// TestSessionAuthNoUsernameSkipsLogin mirrors gofish's behavior: with no
// Username configured, no auth (basic or session) is attempted at all, even
// when AuthMode=="session".
func TestSessionAuthNoUsernameSkipsLogin(t *testing.T) {
	var sessionPOSTs int
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/", "Id": "RootService", "Name": "Root Service",
			"Systems": {"@odata.id": "/redfish/v1/Systems"},
			"Links": {"Sessions": {"@odata.id": "/redfish/v1/SessionService/Sessions"}}
		}`)
	})
	mux.HandleFunc("/redfish/v1/SessionService/Sessions", func(w http.ResponseWriter, r *http.Request) {
		sessionPOSTs++
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bmc, err := NewBMC(Config{BaseURL: srv.URL, AuthMode: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sessionPOSTs != 0 {
		t.Fatalf("session POSTs = %d, want 0 (no Username configured)", sessionPOSTs)
	}
}

// TestListSensorsPartialChassisFailure proves one failing chassis fetch does
// not discard sensor data successfully read from the others -- fetchCollection
// must return partial results alongside the first error (matching gofish's
// common.GetCollectionObjects), not discard everything on any single member
// failure.
func TestListSensorsPartialChassisFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/", "Id": "RootService", "Name": "Root Service",
			"Systems": {"@odata.id": "/redfish/v1/Systems"},
			"Managers": {"@odata.id": "/redfish/v1/Managers"},
			"Chassis": {"@odata.id": "/redfish/v1/Chassis"}
		}`)
	})
	mux.HandleFunc("/redfish/v1/Systems", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"@odata.id":"/redfish/v1/Systems","Name":"c","Members@odata.count":0,"Members":[]}`)
	})
	mux.HandleFunc("/redfish/v1/Managers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"@odata.id":"/redfish/v1/Managers","Name":"c","Members@odata.count":0,"Members":[]}`)
	})
	mux.HandleFunc("/redfish/v1/Chassis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Chassis", "Name": "Chassis Collection",
			"Members@odata.count": 2,
			"Members": [{"@odata.id": "/redfish/v1/Chassis/Good"}, {"@odata.id": "/redfish/v1/Chassis/Bad"}]
		}`)
	})
	mux.HandleFunc("/redfish/v1/Chassis/Good", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Chassis/Good", "Id": "Good", "Name": "Good Chassis",
			"Sensors": {"@odata.id": "/redfish/v1/Chassis/Good/Sensors"}
		}`)
	})
	mux.HandleFunc("/redfish/v1/Chassis/Good/Sensors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Chassis/Good/Sensors", "Name": "Sensors",
			"Members@odata.count": 1,
			"Members": [{"@odata.id": "/redfish/v1/Chassis/Good/Sensors/Inlet"}]
		}`)
	})
	mux.HandleFunc("/redfish/v1/Chassis/Good/Sensors/Inlet", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Chassis/Good/Sensors/Inlet", "Id": "Inlet", "Name": "Inlet Temp",
			"ReadingType": "Temperature", "Reading": 22.5, "ReadingUnits": "Cel",
			"Status": {"State": "Enabled", "Health": "OK"}
		}`)
	})
	mux.HandleFunc("/redfish/v1/Chassis/Bad", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := openFakeClient(t, srv.URL)
	sensors, err := c.ListSensors(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSensors: %v (partial success from the Good chassis must not be discarded)", err)
	}
	if len(sensors) != 1 || sensors[0].Name != "Inlet Temp" {
		t.Fatalf("sensors = %+v, want 1 sensor named Inlet Temp from the Good chassis", sensors)
	}
}

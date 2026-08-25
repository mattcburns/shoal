package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/profile"
)

func TestListProfiles(t *testing.T) {
	store := profile.NewMemory()
	ctx := context.Background()
	if _, err := store.Save(ctx, models.ProvisioningProfile{
		Ref: "lab-1-ubuntu", ISOBase: "ubuntu.iso", OSFamily: "ubuntu", InstallStrategy: "image_write",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, models.ProvisioningProfile{
		Ref: "lab-2-flatcar", ISOBase: "flatcar.iso", OSFamily: "flatcar", InstallStrategy: "scripted_iso",
	}); err != nil {
		t.Fatal(err)
	}

	s := api.New(config.Config{}, nil).WithProfiles(store)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Profiles []profile.Record `json:"profiles"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 2 {
		t.Fatalf("want 2 profiles, got %+v", body.Profiles)
	}
	if body.Profiles[0].Profile.Ref != "lab-1-ubuntu" || body.Profiles[1].Profile.Ref != "lab-2-flatcar" {
		t.Fatalf("want sorted by ref, got %+v", body.Profiles)
	}
}

func TestListProfilesWithoutStore(t *testing.T) {
	s := api.New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestListProfilesEmptyIsArrayNotNull(t *testing.T) {
	s := api.New(config.Config{}, nil).WithProfiles(profile.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["profiles"] == nil {
		t.Fatal("profiles must be [] not null")
	}
}

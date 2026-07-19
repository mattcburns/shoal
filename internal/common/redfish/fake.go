package redfish

import (
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory BMC for unit tests (no network).
type Fake struct {
	mu sync.Mutex

	OpenErr    error
	Systems    []SystemInfo
	Boot       map[string]BootInfo     // systemID -> boot
	Media      map[string]VirtualMedia // URI -> media
	PowerCalls []string
	// InsertedImage tracks last insert per media URI.
	InsertedImage map[string]string

	// FailNextCleanup forces CleanupMediaAndBoot to fail once.
	FailNextCleanup bool

	// SEL and Sensors are returned by ListSEL / ListSensors (Phase 4 Observe).
	SEL     []SELEntry
	Sensors []SensorSample
	// ListSELErr / ListSensorsErr force errors when set.
	ListSELErr     error
	ListSensorsErr error

	// Screenshot is returned by CaptureScreenshot when set.
	Screenshot    *Screenshot
	ScreenshotErr error
}

// NewFake returns a Fake with one system and one CD virtual media slot.
func NewFake() *Fake {
	return &Fake{
		Systems: []SystemInfo{{
			ID: "1", Name: "System.1", PowerState: "Off", ODataID: "/redfish/v1/Systems/1",
		}},
		Boot: map[string]BootInfo{
			"1": {OverrideEnabled: "Disabled", OverrideTarget: "None"},
		},
		Media: map[string]VirtualMedia{
			"/redfish/v1/Managers/1/VirtualMedia/Cd": {
				URI: "/redfish/v1/Managers/1/VirtualMedia/Cd", Name: "Cd", ID: "Cd", SupportsCD: true,
			},
		},
		InsertedImage: make(map[string]string),
	}
}

// NewFakeDualCD returns a Fake with two CD-capable Virtual Media slots (M3 second_media tests).
func NewFakeDualCD() *Fake {
	f := NewFake()
	f.Media["/redfish/v1/Managers/1/VirtualMedia/Cd2"] = VirtualMedia{
		URI: "/redfish/v1/Managers/1/VirtualMedia/Cd2", Name: "Cd2", ID: "Cd2", SupportsCD: true,
	}
	return f
}

// InsertedImages returns a copy of media URI → image URL (test helper).
func (f *Fake) InsertedImages() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.InsertedImage))
	for k, v := range f.InsertedImage {
		out[k] = v
	}
	return out
}

func (f *Fake) Open(_ context.Context) error  { return f.OpenErr }
func (f *Fake) Close(_ context.Context) error { return nil }

func (f *Fake) ServiceRoot(_ context.Context) (ServiceRoot, error) {
	return ServiceRoot{Name: "Fake", RedfishVersion: "1.0"}, nil
}

func (f *Fake) ListSystems(_ context.Context) ([]SystemInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SystemInfo, len(f.Systems))
	copy(out, f.Systems)
	return out, nil
}

func (f *Fake) GetSystem(_ context.Context, systemID string) (SystemInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.Systems {
		if systemID == "" || s.ID == systemID || s.Name == systemID {
			return s, nil
		}
	}
	return SystemInfo{}, fmt.Errorf("fake: system not found")
}

func (f *Fake) GetBoot(_ context.Context, systemID string) (BootInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if systemID == "" {
		systemID = "1"
	}
	b, ok := f.Boot[systemID]
	if !ok {
		return BootInfo{}, fmt.Errorf("fake: no boot for %s", systemID)
	}
	return b, nil
}

func (f *Fake) SetBootOverrideOnceCD(_ context.Context, systemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if systemID == "" {
		systemID = "1"
	}
	f.Boot[systemID] = BootInfo{OverrideEnabled: "Once", OverrideTarget: "Cd"}
	return nil
}

func (f *Fake) ClearBootOverride(_ context.Context, systemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if systemID == "" {
		systemID = "1"
	}
	f.Boot[systemID] = BootInfo{OverrideEnabled: "Disabled", OverrideTarget: "None"}
	return nil
}

func (f *Fake) ListVirtualMedia(_ context.Context, _ string) ([]VirtualMedia, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]VirtualMedia, 0, len(f.Media))
	for _, m := range f.Media {
		out = append(out, m)
	}
	return out, nil
}

func (f *Fake) InsertVirtualMedia(_ context.Context, mediaURI, imageURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Media[mediaURI]
	if !ok {
		return fmt.Errorf("fake: media not found")
	}
	m.Inserted = true
	m.Image = imageURL
	f.Media[mediaURI] = m
	f.InsertedImage[mediaURI] = imageURL
	return nil
}

func (f *Fake) EjectVirtualMedia(_ context.Context, mediaURI string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Media[mediaURI]
	if !ok {
		return fmt.Errorf("fake: media not found")
	}
	m.Inserted = false
	m.Image = ""
	f.Media[mediaURI] = m
	delete(f.InsertedImage, mediaURI)
	return nil
}

func (f *Fake) Power(_ context.Context, systemID, resetType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PowerCalls = append(f.PowerCalls, resetType)
	for i, s := range f.Systems {
		if systemID == "" || s.ID == systemID {
			s.PowerState = "On"
			f.Systems[i] = s
		}
	}
	return nil
}

func (f *Fake) CleanupMediaAndBoot(ctx context.Context, systemID string) error {
	f.mu.Lock()
	if f.FailNextCleanup {
		f.FailNextCleanup = false
		f.mu.Unlock()
		return fmt.Errorf("fake: cleanup failed")
	}
	f.mu.Unlock()
	vms, _ := f.ListVirtualMedia(ctx, systemID)
	for _, vm := range vms {
		if vm.Inserted {
			_ = f.EjectVirtualMedia(ctx, vm.URI)
		}
	}
	return f.ClearBootOverride(ctx, systemID)
}

func (f *Fake) ListSEL(_ context.Context, _ string, opts SELOptions) ([]SELEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListSELErr != nil {
		return nil, f.ListSELErr
	}
	out := make([]SELEntry, 0, len(f.SEL))
	for _, e := range f.SEL {
		if !opts.Since.IsZero() && !e.Created.IsZero() && e.Created.Before(opts.Since) {
			continue
		}
		out = append(out, e)
	}
	max := opts.MaxEntries
	if max <= 0 {
		max = 200
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func (f *Fake) ListSensors(_ context.Context, _ string) ([]SensorSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListSensorsErr != nil {
		return nil, f.ListSensorsErr
	}
	out := make([]SensorSample, len(f.Sensors))
	copy(out, f.Sensors)
	return out, nil
}

func (f *Fake) CaptureScreenshot(_ context.Context, _ string, kind ScreenshotKind) (Screenshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ScreenshotErr != nil {
		return Screenshot{
			Kind: kind,
			Debug: []CaptureDebugStep{{
				Phase: "fake", OK: false, Message: f.ScreenshotErr.Error(),
			}},
		}, f.ScreenshotErr
	}
	if f.Screenshot != nil {
		s := *f.Screenshot
		if s.Kind == "" {
			s.Kind = kind
		}
		return s, nil
	}
	return Screenshot{
		Kind: kind,
		Debug: []CaptureDebugStep{{
			Phase: "fake", OK: false,
			Message: "fake BMC has no screenshot (set Fake.Screenshot for tests)",
		}},
	}, fmt.Errorf("fake: screenshot not configured")
}

// MediaInserted reports whether any media is inserted (test helper).
func (f *Fake) MediaInserted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.Media {
		if m.Inserted {
			return true
		}
	}
	return false
}

// BootCleared reports whether boot override is disabled on system 1.
func (f *Fake) BootCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.Boot["1"]
	return b.OverrideEnabled == "Disabled"
}

var _ BMC = (*Fake)(nil)

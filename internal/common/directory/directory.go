// Package directory defines the device-directory abstraction: a single
// Store interface for looking up and mutating device identity records,
// backed either by a local JSON file store (this package's FileStore) or,
// in a sibling unit, a NetBox adapter. Both backends are always compiled
// into the binary; which one a running server uses is a runtime config
// gate (SHOAL_DEVICE_STORE_DIR / SHOAL_NETBOX_URL), never a build tag.
//
// NOTE: this file is a minimal scaffold added by the api-devices-endpoint
// unit (GET/POST /v1/devices) so that unit's PR compiles and is testable
// standalone ahead of the "directory-interface" sibling unit landing. The
// Store contract below matches that sibling's documented shape exactly;
// the coordinator reconciles any duplication/refinement at merge time.
package directory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mattcburns/shoal/internal/common/models"
)

// ErrNotFound is returned by GetDevice, ResolveDeviceID, SetLifecycle, and
// DeleteDevice when the requested device does not exist.
var ErrNotFound = errors.New("directory: device not found")

// Store is the device-directory abstraction shared by every caller that
// needs to look up or mutate device identity records (the API's
// GET/POST /v1/devices, Discover ingest, Deploy Orchestrator lifecycle
// writes, ...), independent of which backend (local file store or NetBox)
// is actually configured.
type Store interface {
	ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)
	GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)
	UpsertDevice(ctx context.Context, d models.DeviceIdentity) (string, error)
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
	ResolveDeviceID(ctx context.Context, key string) (string, error)
	DeleteDevice(ctx context.Context, id string) error
}

// FileStore is a JSON-file-backed Store: one file per device, named by id,
// under a directory. Mirrors internal/core/profile.FileStore's shape
// (mutex-guarded, atomic tmp+rename writes).
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore creates dir if needed (mode 0700).
func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("directory: empty store directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("directory: mkdir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(id string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, id)
	return filepath.Join(f.dir, safe+".json")
}

// ListDevices returns every stored device, sorted by id.
func (f *FileStore) ListDevices(_ context.Context) ([]models.DeviceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, fmt.Errorf("directory: list: %w", err)
	}
	out := make([]models.DeviceIdentity, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		d, err := f.readFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetDevice loads a device by id.
func (f *FileStore) GetDevice(_ context.Context, id string) (models.DeviceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readFile(f.path(id))
}

// UpsertDevice creates (empty ID) or replaces (non-empty ID) a device
// record and returns its id. A new record with no LifecycleState defaults
// to models.StateDiscovered.
func (f *FileStore) UpsertDevice(_ context.Context, d models.DeviceIdentity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(d.ID) == "" {
		d.ID = newDeviceID()
	}
	if d.LifecycleState == "" {
		d.LifecycleState = models.StateDiscovered
	}
	if err := f.writeFile(f.path(d.ID), d); err != nil {
		return "", err
	}
	return d.ID, nil
}

// SetLifecycle updates the lifecycle state of the device resolved from key
// (id, serial, or name; see ResolveDeviceID).
func (f *FileStore) SetLifecycle(_ context.Context, deviceKey string, state models.LifecycleState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, err := f.resolveLocked(deviceKey)
	if err != nil {
		return err
	}
	d, err := f.readFile(f.path(id))
	if err != nil {
		return err
	}
	d.LifecycleState = state
	return f.writeFile(f.path(id), d)
}

// ResolveDeviceID maps an id, serial, or name to a durable device id.
func (f *FileStore) ResolveDeviceID(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolveLocked(key)
}

func (f *FileStore) resolveLocked(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrNotFound
	}
	if _, err := f.readFile(f.path(key)); err == nil {
		return key, nil
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return "", fmt.Errorf("directory: resolve: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		d, err := f.readFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		if d.Serial == key || d.Name == key {
			return d.ID, nil
		}
	}
	return "", ErrNotFound
}

// DeleteDevice removes a device record by id.
func (f *FileStore) DeleteDevice(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.path(id)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (f *FileStore) readFile(path string) (models.DeviceIdentity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return models.DeviceIdentity{}, ErrNotFound
		}
		return models.DeviceIdentity{}, err
	}
	var d models.DeviceIdentity
	if err := json.Unmarshal(b, &d); err != nil {
		return models.DeviceIdentity{}, fmt.Errorf("directory: decode: %w", err)
	}
	return d, nil
}

func (f *FileStore) writeFile(path string, d models.DeviceIdentity) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newDeviceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "dev-" + hex.EncodeToString(b[:])
}

// Memory is an in-process Store for tests and for a server instance that
// hasn't configured a persistent backend (SHOAL_DEVICE_STORE_DIR unset,
// no NetBox) -- mirrors internal/core/profile.Memory's role for profiles,
// and internal/cli's secrets.NewMemory() fallback for the secrets backend.
type Memory struct {
	mu     sync.Mutex
	byID   map[string]models.DeviceIdentity
	nextID int
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory {
	return &Memory{byID: map[string]models.DeviceIdentity{}}
}

// ListDevices implements Store.
func (m *Memory) ListDevices(_ context.Context) ([]models.DeviceIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.DeviceIdentity, 0, len(m.byID))
	for _, d := range m.byID {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetDevice implements Store.
func (m *Memory) GetDevice(_ context.Context, id string) (models.DeviceIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.byID[id]
	if !ok {
		return models.DeviceIdentity{}, ErrNotFound
	}
	return d, nil
}

// UpsertDevice implements Store.
func (m *Memory) UpsertDevice(_ context.Context, d models.DeviceIdentity) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(d.ID) == "" {
		m.nextID++
		d.ID = fmt.Sprintf("dev-mem-%d", m.nextID)
	}
	if d.LifecycleState == "" {
		d.LifecycleState = models.StateDiscovered
	}
	m.byID[d.ID] = d
	return d.ID, nil
}

// SetLifecycle implements Store.
func (m *Memory) SetLifecycle(_ context.Context, deviceKey string, state models.LifecycleState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.resolveLocked(deviceKey)
	if err != nil {
		return err
	}
	d := m.byID[id]
	d.LifecycleState = state
	m.byID[id] = d
	return nil
}

// ResolveDeviceID implements Store.
func (m *Memory) ResolveDeviceID(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolveLocked(key)
}

func (m *Memory) resolveLocked(key string) (string, error) {
	if _, ok := m.byID[key]; ok {
		return key, nil
	}
	for _, d := range m.byID {
		if d.Serial == key || d.Name == key {
			return d.ID, nil
		}
	}
	return "", ErrNotFound
}

// DeleteDevice implements Store.
func (m *Memory) DeleteDevice(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

var _ Store = (*FileStore)(nil)
var _ Store = (*Memory)(nil)

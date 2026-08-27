package directory

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// ErrNotFound is returned by GetDevice, DeleteDevice, ResolveDeviceID, and
// SetLifecycle when the requested id/key does not resolve to a stored
// device. Implementations should wrap it with fmt.Errorf's %w so callers
// can use errors.Is(err, directory.ErrNotFound).
var ErrNotFound = errors.New("directory: device not found")

// Store is Shoal's device-directory abstraction. It is satisfied by both
// FileStore (this package's local, file-backed implementation) and, via an
// adapter in internal/common/netbox (a sibling unit), NetBox. Exactly one
// backend is selected at runtime by configuration; both are always compiled
// into the binary.
//
// UpsertDevice, SetLifecycle, and ResolveDeviceID intentionally share their
// exact signatures with netbox.API, netbox.LifecycleWriter, and
// netbox.DeviceResolver, so any Store also satisfies those narrower
// interfaces without any change to their existing consumers.
//
// See the package doc for the id-vs-key distinction between
// GetDevice/DeleteDevice (literal id) and SetLifecycle/ResolveDeviceID
// (flexible serial/name/id key), and for error-handling conventions.
type Store interface {
	// ListDevices returns every stored device. It returns an empty (possibly
	// nil-backed but always non-nil-length-safe, i.e. safe to range over)
	// slice and a nil error when the store holds no devices -- never
	// ErrNotFound.
	ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)

	// GetDevice loads a device by its literal id (as returned by
	// UpsertDevice or ResolveDeviceID). Returns ErrNotFound if no device
	// with that id is stored.
	GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)

	// UpsertDevice creates or updates a device and returns its id.
	//
	// If d.ID is non-empty, that id is used verbatim (creating a new record
	// if it doesn't already exist, or overwriting the existing one if it
	// does). If d.ID is empty, an id is derived: an existing device
	// matching d.Serial (when d.Serial is non-empty) is reused so repeated
	// upserts of the same physical device update in place rather than
	// duplicate; otherwise a new id is derived from d.Serial when present,
	// or generated when not.
	UpsertDevice(ctx context.Context, d models.DeviceIdentity) (string, error)

	// SetLifecycle updates only the lifecycle_state of the device matched
	// by deviceKey (resolved serial-first, then name, then id -- see
	// ResolveDeviceID). Returns ErrNotFound if deviceKey does not resolve.
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error

	// ResolveDeviceID maps an operator-facing device key -- a serial, a
	// name, or an id -- to the stable id Shoal should use as device_id.
	// Lookup order: serial match, then name match, then (only if key
	// already names a stored device) key itself. Returns ErrNotFound if
	// none of those match.
	ResolveDeviceID(ctx context.Context, key string) (string, error)

	// DeleteDevice removes the device with the given literal id. Returns
	// ErrNotFound if no device with that id is stored.
	DeleteDevice(ctx context.Context, id string) error
}

// FileStore is a local, JSON-file-backed Store implementation requiring no
// extra infrastructure -- the local-store counterpart to NetBox.
//
// On-disk layout: a single index file, "devices.json", directly under the
// directory passed to NewFileStore. It holds a JSON object mapping each
// device's id to its models.DeviceIdentity. This (rather than one file per
// device) was chosen because it makes ResolveDeviceID's serial/name lookups
// and ListDevices simple in-memory operations with a single atomic file
// write per mutation, at the cost of rewriting the whole file on every
// change -- an acceptable trade-off for the device counts a directory of
// bare-metal lab/rack hardware implies.
//
// FileStore keeps the full index in memory behind a sync.RWMutex and
// persists it after every mutating call via write-to-temp-then-rename, so
// a crash mid-write never leaves devices.json truncated or corrupt. It is
// safe for concurrent use from multiple goroutines within one process; it
// does not coordinate across processes (no file locking), matching
// internal/core/profile.FileStore's documented scope.
type FileStore struct {
	path string
	mu   sync.RWMutex
	// devices is keyed by DeviceIdentity.ID.
	devices map[string]models.DeviceIdentity
}

// NewFileStore creates dir (mode 0700) if needed, loads devices.json from
// it if present, and returns a ready-to-use FileStore. A missing
// devices.json is not an error -- the store simply starts empty.
func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("directory: empty store directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("directory: mkdir: %w", err)
	}
	fs := &FileStore{
		path:    filepath.Join(dir, "devices.json"),
		devices: map[string]models.DeviceIdentity{},
	}
	if err := fs.load(); err != nil {
		return nil, err
	}
	return fs, nil
}

func (f *FileStore) load() error {
	b, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("directory: read: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	var m map[string]models.DeviceIdentity
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("directory: decode: %w", err)
	}
	// A devices.json containing the literal JSON value "null" unmarshals
	// successfully into a nil map; guard against that so f.devices is
	// always a non-nil map ready for assignment.
	if m == nil {
		m = map[string]models.DeviceIdentity{}
	}
	f.devices = m
	return nil
}

// persist must be called with f.mu held (read or write lock doesn't matter
// for correctness of the write itself, but callers hold the write lock
// since persist always follows a mutation).
func (f *FileStore) persist() error {
	b, err := json.MarshalIndent(f.devices, "", "  ")
	if err != nil {
		return fmt.Errorf("directory: encode: %w", err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("directory: write: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("directory: rename: %w", err)
	}
	return nil
}

// ListDevices implements Store.
func (f *FileStore) ListDevices(_ context.Context) ([]models.DeviceIdentity, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]models.DeviceIdentity, 0, len(f.devices))
	for _, d := range f.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetDevice implements Store.
func (f *FileStore) GetDevice(_ context.Context, id string) (models.DeviceIdentity, error) {
	id = strings.TrimSpace(id)
	f.mu.RLock()
	defer f.mu.RUnlock()
	d, ok := f.devices[id]
	if !ok {
		return models.DeviceIdentity{}, fmt.Errorf("directory: get %q: %w", id, ErrNotFound)
	}
	return d, nil
}

// UpsertDevice implements Store.
func (f *FileStore) UpsertDevice(_ context.Context, d models.DeviceIdentity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Normalize Serial up front: it is both stored and used as a lookup key
	// (findIDBySerialLocked, resolveLocked) via exact string comparison, so
	// incidental whitespace (common in fixed-width hardware serial fields)
	// must not survive into the stored value or a later trimmed-key lookup
	// would silently fail to match it.
	d.Serial = strings.TrimSpace(d.Serial)

	id := strings.TrimSpace(d.ID)
	if id == "" {
		if d.Serial != "" {
			// Reuse the id of an existing device with this serial, if any,
			// so repeated upserts of the same physical device update in
			// place rather than create duplicates.
			if existingID := f.findIDBySerialLocked(d.Serial); existingID != "" {
				id = existingID
			} else {
				id = d.Serial
			}
		} else {
			id = generateID()
		}
	}
	d.ID = id

	prev, existed := f.devices[id]
	f.devices[id] = d
	if err := f.persist(); err != nil {
		// Roll back the in-memory map so it never drifts ahead of what's on
		// disk: a caller that saw this error must be able to trust that the
		// upsert did not happen.
		if existed {
			f.devices[id] = prev
		} else {
			delete(f.devices, id)
		}
		return "", err
	}
	return id, nil
}

// SetLifecycle implements Store.
func (f *FileStore) SetLifecycle(_ context.Context, deviceKey string, state models.LifecycleState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.resolveLocked(deviceKey)
	if id == "" {
		return fmt.Errorf("directory: set lifecycle %q: %w", deviceKey, ErrNotFound)
	}
	prev := f.devices[id]
	d := prev
	d.LifecycleState = state
	f.devices[id] = d
	if err := f.persist(); err != nil {
		f.devices[id] = prev
		return err
	}
	return nil
}

// ResolveDeviceID implements Store.
func (f *FileStore) ResolveDeviceID(_ context.Context, key string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	id := f.resolveLocked(key)
	if id == "" {
		return "", fmt.Errorf("directory: resolve %q: %w", key, ErrNotFound)
	}
	return id, nil
}

// DeleteDevice implements Store.
func (f *FileStore) DeleteDevice(_ context.Context, id string) error {
	id = strings.TrimSpace(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	prev, ok := f.devices[id]
	if !ok {
		return fmt.Errorf("directory: delete %q: %w", id, ErrNotFound)
	}
	delete(f.devices, id)
	if err := f.persist(); err != nil {
		f.devices[id] = prev
		return err
	}
	return nil
}

// resolveLocked implements the serial -> name -> id-as-is lookup order
// shared by SetLifecycle and ResolveDeviceID. Caller must hold f.mu (read
// or write lock). Returns "" if key resolves to nothing stored.
func (f *FileStore) resolveLocked(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if id := f.findIDBySerialLocked(key); id != "" {
		return id
	}
	for _, d := range f.devices {
		if d.Name != "" && d.Name == key {
			return d.ID
		}
	}
	if _, ok := f.devices[key]; ok {
		return key
	}
	return ""
}

func (f *FileStore) findIDBySerialLocked(serial string) string {
	for _, d := range f.devices {
		if d.Serial != "" && d.Serial == serial {
			return d.ID
		}
	}
	return ""
}

// idFallbackCounter guarantees generateID's crypto/rand-failure fallback is
// unique within this process even across back-to-back calls in the same
// nanosecond.
var idFallbackCounter uint64

// generateID returns a fresh random id for a device with no serial to
// derive one from (e.g. "dev-3f9a2c7b1e8d4a6f").
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err == nil {
		return fmt.Sprintf("dev-%x", b)
	}
	// crypto/rand.Read failing is effectively unrecoverable and vanishingly
	// rare; fall back to a monotonic per-process counter plus a wall-clock
	// timestamp rather than anything address-based (the Go allocator can
	// hand back the same address for two different calls' now-freed
	// slices, which would silently collide and overwrite an existing
	// device).
	n := atomic.AddUint64(&idFallbackCounter, 1)
	return fmt.Sprintf("dev-fallback-%d-%d", time.Now().UnixNano(), n)
}

var _ Store = (*FileStore)(nil)

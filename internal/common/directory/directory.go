// Package directory defines Store, the device-directory abstraction shared
// by Shoal's CLI/API composition roots so callers can look up and mutate
// device records without caring whether the backend is the NetBox plugin or
// a local file-backed store; the concrete backend is selected at runtime by
// config, never by build tag.
//
// STUB NOTICE: this package is owned by the sibling "directory-interface"
// work unit, which had not landed in this branch's tree at the time the
// cli-directory-wiring unit (internal/cli composition-root rewiring) was
// implemented. The Store interface and NewFileStore below are a minimal,
// functional placeholder written only so internal/cli could compile call
// the documented shape end-to-end and be exercised by a live smoke test.
// At merge time this file should be replaced wholesale by the sibling
// unit's real implementation -- callers only depend on the exported Store
// interface and the NewFileStore(dir) (*FileStore, error) constructor
// signature, both reproduced here to match the coordinator's documented
// contract, so no call-site changes should be needed in internal/cli when
// that swap happens.
package directory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Store is the device-directory abstraction implemented by both the NetBox
// adapter and the local FileStore. It is signature-identical to (the union
// of) Shoal's existing netbox.API / netbox.LifecycleWriter /
// netbox.DeviceResolver interfaces, so a Store value can be passed directly
// into any field/parameter typed with those narrower interfaces.
type Store interface {
	ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)
	GetDevice(ctx context.Context, key string) (models.DeviceIdentity, error)
	UpsertDevice(ctx context.Context, id models.DeviceIdentity) (string, error)
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
	ResolveDeviceID(ctx context.Context, key string) (string, error)
	DeleteDevice(ctx context.Context, key string) error
}

// FileStore is a local, file-backed Store: one JSON file per device in dir.
// It requires no external services, so it is always available as the
// no-NetBox-configured fallback.
//
// KNOWN LIMITATION (stub): mu only serializes access within this process.
// Nothing here guards against a second OS process (e.g. `shoal serve`
// running in the background and a one-shot `shoal deploy *` CLI invocation)
// pointed at the same dir concurrently read-modify-writing the same
// device's file -- the loser of such a race silently loses its update.
// Writes are atomic per-file (writeJSON temp+rename), so this can't corrupt
// a record, only lose a whole update to a last-writer-wins race. A real
// fix needs cross-process locking (e.g. flock on a lockfile), which this
// temporary stub deliberately doesn't add -- see the package doc comment.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore opens (creating if necessary) a directory-backed Store
// rooted at dir. The directory is created on demand so callers don't need
// to pre-provision it (e.g. a fresh SHOAL_DEVICE_STORE_DIR on first boot).
func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("directory: dir required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("directory: mkdir %s: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(id string) string {
	return filepath.Join(f.dir, sanitizeID(id)+".json")
}

// sanitizeID turns a device key into a filesystem-safe filename stem. Keys
// made up only of "safe" characters pass through under an "s_" prefix (kept
// mostly readable); anything else is hex-encoded in full under an "x_"
// prefix, rather than having unsafe characters individually replaced -- a
// per-character replace (e.g. ':' -> '_') is not injective and can map two
// distinct keys (e.g. "AA:BB:CC" and "AA_BB_CC") onto the same file, silently
// overwriting one device's record with another's. The two prefixes are
// disjoint and neither the passthrough nor the hex encoding can produce the
// other's prefix, so the two namespaces can never collide with each other,
// and hex-encoding within the "x_" namespace is itself injective -- so no
// two distinct keys can ever map to the same filename.
func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "x_"
	}
	safe := true
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			safe = false
			break
		}
	}
	if safe {
		return "s_" + id
	}
	return "x_" + hex.EncodeToString([]byte(id))
}

// writeJSON marshals v and writes it to path atomically (write to a temp
// file in the same directory, then rename) so a crash mid-write leaves
// either the old file or the new one intact -- never truncated/corrupt
// JSON that ListDevices/findLocked would otherwise silently skip.
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("directory: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("directory: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("directory: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("directory: write: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("directory: rename: %w", err)
	}
	return nil
}

// ListDevices returns every device record in the store, sorted by ID.
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
		raw, err := os.ReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		var d models.DeviceIdentity
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// findLocked scans all records for one matching id, serial, or name. Caller
// must hold f.mu.
func (f *FileStore) findLocked(key string) (models.DeviceIdentity, bool) {
	if raw, err := os.ReadFile(f.path(key)); err == nil {
		var d models.DeviceIdentity
		if json.Unmarshal(raw, &d) == nil {
			return d, true
		}
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return models.DeviceIdentity{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		var d models.DeviceIdentity
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		if d.Serial == key || d.Name == key || d.ID == key {
			return d, true
		}
	}
	return models.DeviceIdentity{}, false
}

// GetDevice loads identity by id, serial, or name.
func (f *FileStore) GetDevice(_ context.Context, key string) (models.DeviceIdentity, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return models.DeviceIdentity{}, fmt.Errorf("directory: device key required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(key)
	if !ok {
		return models.DeviceIdentity{}, fmt.Errorf("directory: device %q not found", key)
	}
	return d, nil
}

// UpsertDevice finds a device by serial (or id) and updates it, or creates a
// new record. Returns the record's id.
func (f *FileStore) UpsertDevice(_ context.Context, id models.DeviceIdentity) (string, error) {
	if strings.TrimSpace(id.Serial) == "" && strings.TrimSpace(id.ID) == "" {
		return "", fmt.Errorf("directory: serial or id required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	key := id.ID
	if key == "" {
		key = id.Serial
	}
	if existing, ok := f.findLocked(key); ok {
		// Preserve the existing record's id and any fields the incoming
		// value leaves blank, so a partial upsert (e.g. a re-ingest that
		// only carries identity fields, not credential_ref/bmc_ip) doesn't
		// silently wipe data set by an earlier, more complete upsert.
		if id.ID == "" {
			id.ID = existing.ID
		}
		if id.Serial == "" {
			id.Serial = existing.Serial
		}
		if id.Name == "" {
			id.Name = existing.Name
		}
		if id.Vendor == "" {
			id.Vendor = existing.Vendor
		}
		if id.Model == "" {
			id.Model = existing.Model
		}
		if id.LifecycleState == "" {
			id.LifecycleState = existing.LifecycleState
		}
		if id.CredentialRef == "" {
			id.CredentialRef = existing.CredentialRef
		}
		if id.BMCIP == "" {
			id.BMCIP = existing.BMCIP
		}
	}
	if id.ID == "" {
		id.ID = sanitizeID(id.Serial)
	}
	if err := writeJSON(f.path(id.ID), id); err != nil {
		return "", err
	}
	return id.ID, nil
}

// SetLifecycle updates lifecycle_state for a device identified by id, serial, or name.
func (f *FileStore) SetLifecycle(_ context.Context, deviceKey string, state models.LifecycleState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(deviceKey)
	if !ok {
		return fmt.Errorf("directory: device %q not found", deviceKey)
	}
	d.LifecycleState = state
	return writeJSON(f.path(d.ID), d)
}

// ResolveDeviceID maps a device key (id, serial, or name) to its stored id.
// When no record matches, the key is returned unchanged (matching
// netbox.Client.ResolveDeviceID's behavior of assuming an unmatched key is
// already the canonical id).
func (f *FileStore) ResolveDeviceID(_ context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("directory: device key required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.findLocked(key); ok {
		return d.ID, nil
	}
	return key, nil
}

// SetCredentialRef writes credential_ref (and optional bmc_ip) for a device
// identified by id, serial, or name. It never creates a device. This isn't
// part of the Store interface (matching the documented directory.Store
// contract, which doesn't include it either) but mirrors
// netbox.Client.SetCredentialRef's signature/semantics so FileStore can
// satisfy the same ad hoc deviceNB-shaped assertions the CLI composition
// root uses for device-credential wiring.
func (f *FileStore) SetCredentialRef(_ context.Context, deviceKey, ref, bmcIP string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("directory: credential_ref required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(deviceKey)
	if !ok {
		return fmt.Errorf("directory: device %q not found", deviceKey)
	}
	d.CredentialRef = ref
	if ip := strings.TrimSpace(bmcIP); ip != "" {
		d.BMCIP = ip
	}
	return writeJSON(f.path(d.ID), d)
}

// DeleteDevice removes a device record identified by id, serial, or name.
func (f *FileStore) DeleteDevice(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(key)
	if !ok {
		return fmt.Errorf("directory: device %q not found", key)
	}
	if err := os.Remove(f.path(d.ID)); err != nil {
		return fmt.Errorf("directory: remove: %w", err)
	}
	return nil
}

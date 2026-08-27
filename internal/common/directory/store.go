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

// ErrNotFound is returned when a device lookup finds no matching record.
var ErrNotFound = errors.New("directory: not found")

// Store is the device-identity directory backend. This is a cross-unit
// contract: internal/ui codes directly against it.
type Store interface {
	// ListDevices returns every device, in a stable order.
	ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)
	// GetDevice returns one device by its canonical ID, or ErrNotFound.
	GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)
	// UpsertDevice creates (d.ID == "") or updates (d.ID != "") a device record
	// and returns its canonical ID.
	UpsertDevice(ctx context.Context, d models.DeviceIdentity) (string, error)
	// SetLifecycle updates only the lifecycle_state field for the device
	// resolved from deviceKey (see ResolveDeviceID).
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
	// ResolveDeviceID resolves a human-facing key (ID, name, or serial) to the
	// device's canonical ID, or ErrNotFound.
	ResolveDeviceID(ctx context.Context, key string) (string, error)
	// DeleteDevice removes a device record by canonical ID.
	DeleteDevice(ctx context.Context, id string) error
}

// FileStore is a JSON-file-backed Store: one file per device under dir.
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
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
	return filepath.Join(f.dir, safe+".json")
}

// ListDevices implements Store.
func (f *FileStore) ListDevices(_ context.Context) ([]models.DeviceIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("directory: read dir: %w", err)
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// GetDevice implements Store.
func (f *FileStore) GetDevice(_ context.Context, id string) (models.DeviceIdentity, error) {
	if strings.TrimSpace(id) == "" {
		return models.DeviceIdentity{}, ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readFile(f.path(id))
}

// UpsertDevice implements Store.
func (f *FileStore) UpsertDevice(_ context.Context, d models.DeviceIdentity) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(d.ID) == "" {
		id, err := newDeviceID()
		if err != nil {
			return "", err
		}
		d.ID = id
	}
	if err := f.writeFile(f.path(d.ID), d); err != nil {
		return "", err
	}
	return d.ID, nil
}

// SetLifecycle implements Store.
func (f *FileStore) SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error {
	id, err := f.ResolveDeviceID(ctx, deviceKey)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, err := f.readFile(f.path(id))
	if err != nil {
		return err
	}
	d.LifecycleState = state
	return f.writeFile(f.path(id), d)
}

// ResolveDeviceID implements Store.
func (f *FileStore) ResolveDeviceID(_ context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.readFile(f.path(key)); err == nil {
		return key, nil
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("directory: read dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		d, err := f.readFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		if d.Name == key || d.Serial == key {
			return d.ID, nil
		}
	}
	return "", ErrNotFound
}

// DeleteDevice implements Store.
func (f *FileStore) DeleteDevice(_ context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.path(id)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("directory: delete: %w", err)
	}
	return nil
}

func (f *FileStore) readFile(path string) (models.DeviceIdentity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return models.DeviceIdentity{}, ErrNotFound
		}
		return models.DeviceIdentity{}, fmt.Errorf("directory: read: %w", err)
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
		return fmt.Errorf("directory: encode: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("directory: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("directory: rename: %w", err)
	}
	return nil
}

func newDeviceID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("directory: id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

var _ Store = (*FileStore)(nil)

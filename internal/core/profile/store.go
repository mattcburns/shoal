package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Record is a durable profile plus optional operator approval.
type Record struct {
	Profile    models.ProvisioningProfile `json:"profile"`
	ApprovedAt *time.Time                 `json:"approved_at,omitempty"`
	ApprovedBy string                     `json:"approved_by,omitempty"`
	CreatedAt  time.Time                  `json:"created_at"`
	UpdatedAt  time.Time                  `json:"updated_at"`
}

// NeedsOperatorApproval reports whether Deploy Start must see approval or ApproveDestruct.
func (r Record) NeedsOperatorApproval() bool {
	if r.Profile.NeedsApproval || len(r.Profile.DestructSteps) > 0 {
		return r.ApprovedAt == nil
	}
	return false
}

// Store persists profiles outside NetBox (file-backed).
type Store interface {
	Save(ctx context.Context, p models.ProvisioningProfile) (Record, error)
	Get(ctx context.Context, ref string) (Record, error)
	List(ctx context.Context) ([]Record, error)
	Approve(ctx context.Context, ref, approvedBy string) (Record, error)
}

// FileStore is a JSON-file profile store under a directory.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore creates dir if needed (mode 0700).
func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("profile: empty store directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("profile: mkdir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(ref string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, ref)
	return filepath.Join(f.dir, safe+".json")
}

// Save validates and writes the profile (preserves approval if same ref).
func (f *FileStore) Save(_ context.Context, p models.ProvisioningProfile) (Record, error) {
	if err := ProvisioningProfile(p); err != nil {
		return Record{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	rec := Record{Profile: p, CreatedAt: now, UpdatedAt: now}
	// Preserve approval when overwriting same ref with non-destruct change.
	if prev, err := f.readFile(f.path(p.Ref)); err == nil {
		rec.CreatedAt = prev.CreatedAt
		if prev.ApprovedAt != nil && !p.NeedsApproval && len(p.DestructSteps) == 0 {
			rec.ApprovedAt = prev.ApprovedAt
			rec.ApprovedBy = prev.ApprovedBy
		}
	}
	if err := f.writeFile(f.path(p.Ref), rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Get loads a profile by ref.
func (f *FileStore) Get(_ context.Context, ref string) (Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readFile(f.path(ref))
}

// List returns all stored profiles sorted by ref.
func (f *FileStore) List(_ context.Context) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := f.readFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Profile.Ref < out[j].Profile.Ref
	})
	return out, nil
}

// Approve marks a profile approved by an operator identity string.
func (f *FileStore) Approve(_ context.Context, ref, approvedBy string) (Record, error) {
	if strings.TrimSpace(approvedBy) == "" {
		approvedBy = "operator"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, err := f.readFile(f.path(ref))
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	rec.ApprovedAt = &now
	rec.ApprovedBy = approvedBy
	rec.UpdatedAt = now
	if err := f.writeFile(f.path(ref), rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (f *FileStore) readFile(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, fmt.Errorf("profile: not found")
		}
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("profile: decode: %w", err)
	}
	return rec, nil
}

func (f *FileStore) writeFile(path string, rec Record) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu    sync.Mutex
	byRef map[string]Record
}

// NewMemory returns an empty memory store.
func NewMemory() *Memory {
	return &Memory{byRef: map[string]Record{}}
}

// Save implements Store.
func (m *Memory) Save(_ context.Context, p models.ProvisioningProfile) (Record, error) {
	if err := ProvisioningProfile(p); err != nil {
		return Record{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	rec := Record{Profile: p, CreatedAt: now, UpdatedAt: now}
	if prev, ok := m.byRef[p.Ref]; ok {
		rec.CreatedAt = prev.CreatedAt
	}
	m.byRef[p.Ref] = rec
	return rec, nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, ref string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byRef[ref]
	if !ok {
		return Record{}, fmt.Errorf("profile: not found")
	}
	return rec, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.byRef))
	for _, r := range m.byRef {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Profile.Ref < out[j].Profile.Ref
	})
	return out, nil
}

// Approve implements Store.
func (m *Memory) Approve(_ context.Context, ref, approvedBy string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byRef[ref]
	if !ok {
		return Record{}, fmt.Errorf("profile: not found")
	}
	now := time.Now().UTC()
	rec.ApprovedAt = &now
	if approvedBy == "" {
		approvedBy = "operator"
	}
	rec.ApprovedBy = approvedBy
	rec.UpdatedAt = now
	m.byRef[ref] = rec
	return rec, nil
}

var _ Store = (*FileStore)(nil)
var _ Store = (*Memory)(nil)

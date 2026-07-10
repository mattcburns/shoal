package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// File is a directory-backed Backend. Each credential is a JSON file mode 0600.
type File struct {
	dir string
}

// NewFile creates a file backend under dir (created with 0700 if missing).
func NewFile(dir string) (*File, error) {
	if dir == "" {
		return nil, fmt.Errorf("secrets: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secrets: mkdir: %w", err)
	}
	return &File{dir: dir}, nil
}

func (f *File) path(ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	// ref is a single path segment (ValidateRef rejects separators).
	return filepath.Join(f.dir, ref+".json"), nil
}

// Put writes cred to disk with mode 0600.
func (f *File) Put(_ context.Context, ref string, cred Credential) error {
	p, err := f.path(ref)
	if err != nil {
		return err
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("secrets: marshal: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("secrets: write: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secrets: rename: %w", err)
	}
	// Ensure mode after rename (umask).
	if err := os.Chmod(p, 0o600); err != nil {
		return fmt.Errorf("secrets: chmod: %w", err)
	}
	return nil
}

// Get reads cred from disk.
func (f *File) Get(_ context.Context, ref string) (Credential, error) {
	p, err := f.path(ref)
	if err != nil {
		return Credential{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credential{}, ErrNotFound
		}
		return Credential{}, fmt.Errorf("secrets: read: %w", err)
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return Credential{}, fmt.Errorf("secrets: unmarshal: %w", err)
	}
	return c, nil
}

// Delete removes the credential file.
func (f *File) Delete(_ context.Context, ref string) error {
	p, err := f.path(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("secrets: delete: %w", err)
	}
	return nil
}

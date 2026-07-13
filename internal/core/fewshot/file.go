package fewshot

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileStore is an append-only JSONL store under a directory.
type FileStore struct {
	Dir string
	mu  sync.Mutex
}

// NewFileStore creates a store rooted at dir (must be non-empty).
func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("fewshot: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("fewshot: mkdir: %w", err)
	}
	return &FileStore{Dir: dir}, nil
}

func (s *FileStore) pathFor(prompt string) string {
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, prompt)
	return filepath.Join(s.Dir, name+".learned.jsonl")
}

// Append writes one validated example.
func (s *FileStore) Append(_ context.Context, ex Example) (Example, error) {
	if err := ValidateExample(ex); err != nil {
		return Example{}, err
	}
	if ex.ID == "" {
		ex.ID = newID()
	}
	if ex.CreatedAt.IsZero() {
		ex.CreatedAt = time.Now().UTC()
	}
	line, err := json.Marshal(ex)
	if err != nil {
		return Example{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.pathFor(ex.Prompt)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Example{}, fmt.Errorf("fewshot: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Example{}, fmt.Errorf("fewshot: write: %w", err)
	}
	return ex, nil
}

// Load returns up to limit most recent examples (oldest→newest for prompt reading).
func (s *FileStore) Load(_ context.Context, prompt string, limit int) ([]Example, error) {
	if limit <= 0 {
		limit = DefaultLoadLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.pathFor(prompt)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fewshot: open: %w", err)
	}
	defer f.Close()

	var all []Example
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ex Example
		if err := json.Unmarshal([]byte(line), &ex); err != nil {
			continue
		}
		all = append(all, ex)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var _ Store = (*FileStore)(nil)

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/goccy/go-yaml"
)

// Store holds the active configuration and persists every change to a YAML
// file, so the daemon restarts with the state it had.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  *Config
}

// Open loads path, or starts from def when the file does not exist yet.
func Open(path string, def *Config) (*Store, error) {
	cfg := def
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		cfg = &Config{}
		if err := yaml.UnmarshalWithOptions(b, cfg, yaml.DisallowUnknownField()); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Store{path: path, cfg: cfg}, nil
}

// Get returns a private copy of the configuration.
func (s *Store) Get() *Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.cfg)
}

// Update applies mutate to a copy of the configuration, validates it, hands
// it to apply (which pushes the change to the engine) and only then saves it
// and makes it current. An error at any step leaves the configuration as it
// was.
func (s *Store) Update(mutate func(*Config) error, apply func(old, new *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cfg)
	if err := mutate(next); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := apply(s.cfg, next); err != nil {
		return err
	}
	s.cfg = next
	return s.save()
}

func (s *Store) save() error {
	b, err := yaml.Marshal(s.cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil { // holds session secrets
		return err
	}
	return os.Rename(tmp, s.path)
}

func clone(c *Config) *Config {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // Config holds only plain data
	}
	out := &Config{}
	if err := json.Unmarshal(b, out); err != nil {
		panic(err)
	}
	return out
}

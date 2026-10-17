// Package peers resolves WireGuard peer public keys to human-friendly names.
package peers

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gopkg.in/yaml.v3"
)

// fileFormat is the on-disk representation of the peer names file:
//
//	peers:
//	  "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=": alice-laptop
type fileFormat struct {
	Peers map[string]string `yaml:"peers"`
}

// Store holds a public key -> name mapping that can be reloaded atomically
// while being read concurrently by the collector. The zero value and a nil
// *Store are valid and resolve every key to an empty name.
type Store struct {
	path  string
	names atomic.Pointer[map[wgtypes.Key]string]
}

// NewStore returns a Store backed by the YAML file at path and loads it.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads the backing file. On error the previous mapping is kept.
func (s *Store) Reload() error {
	if s.path == "" {
		return errors.New("peers: no file configured")
	}
	names, err := parseFile(s.path)
	if err != nil {
		return err
	}
	s.names.Store(&names)
	return nil
}

// Len returns the number of known peer names.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	if m := s.names.Load(); m != nil {
		return len(*m)
	}
	return 0
}

// Name returns the friendly name for key, or "" if it is unknown.
func (s *Store) Name(key wgtypes.Key) string {
	if s == nil {
		return ""
	}
	if m := s.names.Load(); m != nil {
		return (*m)[key]
	}
	return ""
}

func parseFile(path string) (map[wgtypes.Key]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("peers: reading %q: %w", path, err)
	}

	var f fileFormat
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("peers: parsing %q: %w", path, err)
	}

	names := make(map[wgtypes.Key]string, len(f.Peers))
	for raw, name := range f.Peers {
		key, err := wgtypes.ParseKey(raw)
		if err != nil {
			return nil, fmt.Errorf("peers: invalid public key %q in %q: %w", raw, path, err)
		}
		names[key] = name
	}
	return names, nil
}

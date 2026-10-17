package peers

import (
	"os"
	"path/filepath"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLoadAndReload(t *testing.T) {
	k1, _ := wgtypes.GeneratePrivateKey()
	k2, _ := wgtypes.GeneratePrivateKey()
	pub1, pub2 := k1.PublicKey(), k2.PublicKey()

	path := filepath.Join(t.TempDir(), "peers.yml")
	writeFile(t, path, "peers:\n  \""+pub1.String()+"\": alice\n")

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Name(pub1); got != "alice" {
		t.Errorf("Name(pub1) = %q, want %q", got, "alice")
	}
	if got := s.Name(pub2); got != "" {
		t.Errorf("Name(pub2) = %q, want empty", got)
	}

	writeFile(t, path, "peers:\n  \""+pub2.String()+"\": bob\n")
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.Name(pub2); got != "bob" {
		t.Errorf("after reload Name(pub2) = %q, want %q", got, "bob")
	}
	if got := s.Name(pub1); got != "" {
		t.Errorf("after reload Name(pub1) = %q, want empty", got)
	}
}

func TestStoreReloadKeepsPreviousOnError(t *testing.T) {
	k, _ := wgtypes.GeneratePrivateKey()
	pub := k.PublicKey()

	path := filepath.Join(t.TempDir(), "peers.yml")
	writeFile(t, path, "peers:\n  \""+pub.String()+"\": alice\n")

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, path, "peers:\n  not-a-key: bob\n")
	if err := s.Reload(); err == nil {
		t.Fatal("expected error for invalid key")
	}
	if got := s.Name(pub); got != "alice" {
		t.Errorf("Name(pub) = %q, want previous value %q", got, "alice")
	}
}

func TestNewStoreErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := NewStore(filepath.Join(dir, "missing.yml")); err == nil {
		t.Error("expected error for missing file")
	}

	bad := filepath.Join(dir, "bad.yml")
	writeFile(t, bad, "peers: [this is: not a map")
	if _, err := NewStore(bad); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if got := s.Name(wgtypes.Key{}); got != "" {
		t.Errorf("nil Store Name() = %q, want empty", got)
	}
	if s.Len() != 0 {
		t.Error("nil Store Len() != 0")
	}
}

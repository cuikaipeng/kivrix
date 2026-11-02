package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo"
)

func TestLocalSecretStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("profile/p1/redis", []byte("s3cret")); err != nil {
		t.Fatal(err)
	}
	vault, err := os.ReadFile(filepath.Join(dir, "secrets.vault"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(vault, []byte("s3cret")) {
		t.Fatal("vault contains the plaintext secret")
	}
	for _, name := range []string{"secrets.vault", "secrets.key"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v %v", name, info.Mode(), err)
		}
	}
	// A fresh store decrypts the persisted value.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Get("profile/p1/redis")
	if err != nil || string(got) != "s3cret" {
		t.Fatalf("get %q %v", got, err)
	}
	if err := again.Delete("profile/p1/redis"); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Get("profile/p1/redis"); !errors.Is(err, mygo.ErrSecretNotFound) {
		t.Fatalf("after delete %v", err)
	}
}

func TestRejectCorruptKey(t *testing.T) {
	for _, size := range []int{0, 1, 31, 33, 100000} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "secrets.key"), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("test", []byte("secret")); err == nil {
			t.Fatalf("accepted %d-byte key", size)
		}
	}
}

func TestTightenExistingSecretFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := t.TempDir()
	s, _ := Open(dir)
	if err := s.Set("test", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"secrets.key", "secrets.vault"} {
		if err := os.Chmod(filepath.Join(dir, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("test"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"secrets.key", "secrets.vault"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("insecure permissions for %s", name)
		}
	}
}

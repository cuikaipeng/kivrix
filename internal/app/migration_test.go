package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyUserData(t *testing.T) {
	parent := t.TempDir()
	oldDir := filepath.Join(parent, "Redis Pilot")
	newDir := filepath.Join(parent, "Kivrix")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"profiles.json":     `[{"id":"p1"}]`,
		"preferences.json":  `{"theme":"Dark"}`,
		"secrets.vault":     "ciphertext",
		"secrets.key":       "key",
		"window-state.json": `{}`,
	} {
		if err := os.WriteFile(filepath.Join(oldDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(newDir, "preferences.json")
	if err := os.WriteFile(keep, []byte(`{"theme":"Light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyUserData(newDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"profiles.json", "secrets.vault", "secrets.key", "window-state.json"} {
		if _, err := os.Stat(filepath.Join(newDir, name)); err != nil {
			t.Fatalf("%s not migrated: %v", name, err)
		}
	}
	data, err := os.ReadFile(keep)
	if err != nil || string(data) != `{"theme":"Light"}` {
		t.Fatalf("new preferences overwritten: %q %v", data, err)
	}
	info, err := os.Stat(filepath.Join(newDir, "secrets.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v %v", info.Mode(), err)
	}
}

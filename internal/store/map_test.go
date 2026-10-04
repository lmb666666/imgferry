package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMapRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "map.json")

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("want empty map, got %v", m.Entries)
	}

	m.Set("https://old/a.png", Entry{NewURL: "https://new/a.png", Name: "a.png"})
	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m2, err := Load(path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	e, ok := m2.Get("https://old/a.png")
	if !ok || e.NewURL != "https://new/a.png" || e.Name != "a.png" {
		t.Fatalf("roundtrip mismatch: %+v ok=%v", e, ok)
	}
}

func TestLoadCorrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map.json")
	os.WriteFile(path, []byte("not json"), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("want error for corrupted map file")
	}
}

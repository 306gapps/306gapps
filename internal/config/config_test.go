package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func store(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: filepath.Join(t.TempDir(), "configs")}
}

func TestSaveAndLoad(t *testing.T) {
	s := store(t)
	in := Config{Name: "My Phone", Packages: []string{"gmscore", "vending"},
		KeepStock: []string{"chrome"}, Release: "17", Target: "recovery"}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("My Phone")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || len(got.Packages) != 2 || got.KeepStock[0] != "chrome" {
		t.Errorf("round trip lost something: %+v", got)
	}
	if got.Created.IsZero() {
		t.Error("a saved config should be stamped")
	}
}

// A name is whatever the user typed, so it has to be safe as a filename.
func TestNamesCannotEscapeTheDirectory(t *testing.T) {
	s := store(t)
	for _, name := range []string{"../../etc/passwd", "/tmp/evil", "a/b/c", "..", "./x"} {
		err := s.Save(Config{Name: name, Packages: []string{"gmscore"}})
		if err != nil {
			continue // refused outright is fine too
		}
		entries, _ := os.ReadDir(s.Dir)
		for _, e := range entries {
			if strings.ContainsAny(e.Name(), `/\`) || strings.Contains(e.Name(), "..") {
				t.Errorf("%q produced an unsafe filename %q", name, e.Name())
			}
		}
	}
	// And nothing landed outside the store.
	if _, err := os.Stat("/tmp/evil.json"); err == nil {
		t.Error("a config escaped the store directory")
	}
}

func TestSaveRefusesTheUseless(t *testing.T) {
	s := store(t)
	if err := s.Save(Config{Name: "", Packages: []string{"gmscore"}}); err == nil {
		t.Error("a config with no name should be refused")
	}
	if err := s.Save(Config{Name: "empty", Packages: nil}); err == nil {
		t.Error("a config that would build nothing should be refused")
	}
	if err := s.Save(Config{Name: "!!!", Packages: []string{"gmscore"}}); err == nil {
		t.Error("a name with nothing usable should be refused")
	}
}

func TestListIsNewestFirst(t *testing.T) {
	s := store(t)
	old := Config{Name: "older", Packages: []string{"gmscore"},
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	recent := Config{Name: "newer", Packages: []string{"gmscore"},
		Created: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.Save(old); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(recent); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "newer" {
		t.Errorf("want newest first, got %v", got)
	}
}

func TestListOnAnEmptyStore(t *testing.T) {
	got, err := store(t).List()
	if err != nil || got != nil {
		t.Errorf("an unused store should list nothing without error: %v %v", got, err)
	}
}

// One corrupt file must not hide every other config.
func TestListSkipsAnUnreadableFile(t *testing.T) {
	s := store(t)
	if err := s.Save(Config{Name: "good", Packages: []string{"gmscore"}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{not json"), 0o644)
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Errorf("a broken file should not hide the rest: %v", got)
	}
}

func TestSaveReplacesTheSameName(t *testing.T) {
	s := store(t)
	s.Save(Config{Name: "same", Packages: []string{"gmscore"}})
	s.Save(Config{Name: "SAME", Packages: []string{"gmscore", "vending"}})
	got, _ := s.List()
	if len(got) != 1 {
		t.Fatalf("a name differing only in case should replace, got %d", len(got))
	}
	if len(got[0].Packages) != 2 {
		t.Error("the later save should win")
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	s := store(t)
	in := Config{Name: "Shared", Packages: []string{"gmscore", "chrome"},
		KeepStock: []string{"chrome"}}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shared.json")
	if err := s.Export("Shared", path); err != nil {
		t.Fatal(err)
	}

	// Somewhere else entirely, as if it had been sent to another person.
	other := store(t)
	got, err := other.Import(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Shared" || len(got.KeepStock) != 1 {
		t.Errorf("import lost something: %+v", got)
	}
	back, err := other.Load("Shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Packages) != 2 {
		t.Error("an imported config should be saved, not just parsed")
	}
}

func TestImportRefusesRubbish(t *testing.T) {
	s := store(t)
	dir := t.TempDir()
	cases := map[string]string{
		"notjson.json": "{",
		"noname.json":  `{"schema":1,"packages":["gmscore"]}`,
		"future.json":  `{"schema":99,"name":"x","packages":["gmscore"]}`,
		"unknown.json": `{"schema":1,"name":"x","packages":["gmscore"],"surprise":true}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := s.Import(p); err == nil {
			t.Errorf("%s should have been refused", name)
		}
	}
}

func TestDelete(t *testing.T) {
	s := store(t)
	s.Save(Config{Name: "gone", Packages: []string{"gmscore"}})
	if err := s.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.Delete("never existed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting nothing should say so, got %v", err)
	}
}

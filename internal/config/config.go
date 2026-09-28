// Package config stores the user's own named selections so a build can be repeated.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Schema is the on-disk version; a config written by a newer build is refused.
const Schema = 1

type Config struct {
	Schema int    `json:"schema"`
	Name   string `json:"name"`
	// Release is a version ("17"), a release id, or empty for whatever is newest.
	Release  string   `json:"release,omitempty"`
	Packages []string `json:"packages"`
	// KeepStock lists packages whose removals are skipped, leaving the ROM's own app in place.
	KeepStock []string  `json:"keep_stock,omitempty"`
	Target    string    `json:"target,omitempty"`
	Created   time.Time `json:"created"`
}

// ErrNotFound is returned for a config that is not saved.
var ErrNotFound = errors.New("no such config")

// Store is a directory of saved configs.
type Store struct{ Dir string }

// New returns the store under the user's config directory, creating nothing until a save.
func New() *Store { return &Store{Dir: filepath.Join(userConfigDir(), "configs")} }

func userConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "306gapps")
	}
	return ".306gapps"
}

// slug turns a display name into a filename. Names are user input, so anything
// outside the safe set becomes a dash rather than risking a path escape.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}

// Save writes a config, replacing one of the same name.
func (s *Store) Save(c Config) error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("a config needs a name")
	}
	id := slug(c.Name)
	if id == "" {
		return fmt.Errorf("%q has no characters usable in a filename", c.Name)
	}
	if len(c.Packages) == 0 {
		return errors.New("a config with no packages would build nothing")
	}
	c.Schema = Schema
	if c.Created.IsZero() {
		c.Created = time.Now().UTC().Truncate(time.Second)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, id+".json"), append(b, '\n'), 0o644)
}

// List returns every saved config, newest first.
func (s *Store) List() ([]Config, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Config
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		c, err := readFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			// One unreadable file should not hide the rest.
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Load returns one config by its display name.
func (s *Store) Load(name string) (Config, error) {
	c, err := readFile(filepath.Join(s.Dir, slug(name)+".json"))
	if os.IsNotExist(err) {
		return Config{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return c, err
}

// Delete removes a saved config.
func (s *Store) Delete(name string) error {
	err := os.Remove(filepath.Join(s.Dir, slug(name)+".json"))
	if os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return err
}

// Export writes a config to a path of the user's choosing.
func (s *Store) Export(name, path string) error {
	c, err := s.Load(name)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Import reads a config from a file and saves it under the name inside the file.
func (s *Store) Import(path string) (Config, error) {
	c, err := readFile(path)
	if err != nil {
		return Config{}, err
	}
	return c, s.Save(c)
}

func readFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if c.Schema > Schema {
		return Config{}, fmt.Errorf("%s was written by a newer 306gapps (schema %d, this build understands %d)",
			filepath.Base(path), c.Schema, Schema)
	}
	if strings.TrimSpace(c.Name) == "" {
		return Config{}, fmt.Errorf("%s has no name", filepath.Base(path))
	}
	return c, nil
}

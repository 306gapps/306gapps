package presets

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
)

type stub struct {
	body string
	err  error
}

func (s stub) Open(context.Context, string) (io.ReadCloser, int64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), int64(len(s.body)), nil
}

func TestBuiltinParses(t *testing.T) {
	defs := Builtin()
	if len(defs) == 0 {
		t.Fatal("no presets")
	}
	ids := map[string]bool{}
	for _, d := range defs {
		if d.Name == "" {
			t.Errorf("%s has no name", d.ID)
		}
		ids[d.ID] = true
	}
	for _, want := range []string{"core", "stock", "full", "everything", "crdroid"} {
		if !ids[want] {
			t.Errorf("missing preset %q", want)
		}
	}
}

// The bug this package exists to make fixable: crDroid's config switches the
// launcher off, and both its presets have to agree.
func TestCrdroidPresetsLeaveTheLauncherOut(t *testing.T) {
	for _, d := range Builtin() {
		if !strings.HasPrefix(d.ID, "crdroid") {
			continue
		}
		for _, p := range d.Packages {
			if p == "pixellauncher" {
				t.Errorf("%s includes pixellauncher", d.ID)
			}
		}
	}
}

func TestResolveFlattensTiersInManifestOrder(t *testing.T) {
	defs := []Definition{
		{ID: "core", Name: "Core", Packages: []string{"vending", "gmscore"}},
		{ID: "basic", Name: "Basic", Includes: "core", Packages: []string{"dialer"}},
	}
	got, err := Resolve(defs, []string{"gmscore", "vending", "dialer"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gmscore", "vending", "dialer"}; !eq(got[1].Packages, want) {
		t.Errorf("basic = %v, want %v", got[1].Packages, want)
	}
}

func TestResolveDropsPackagesTheReleaseLacks(t *testing.T) {
	defs := []Definition{{ID: "x", Name: "X", Packages: []string{"gmscore", "gemini"}}}
	got, _ := Resolve(defs, []string{"gmscore"})
	if !eq(got[0].Packages, []string{"gmscore"}) {
		t.Errorf("got %v", got[0].Packages)
	}
}

func TestResolveAllTakesEverything(t *testing.T) {
	defs := []Definition{{ID: "everything", Name: "Everything", All: true}}
	got, _ := Resolve(defs, []string{"a", "b", "c"})
	if !eq(got[0].Packages, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got[0].Packages)
	}
}

func TestResolveRefusesACycle(t *testing.T) {
	defs := []Definition{
		{ID: "a", Name: "A", Includes: "b"},
		{ID: "b", Name: "B", Includes: "a"},
	}
	if _, err := Resolve(defs, []string{"x"}); err == nil {
		t.Fatal("want an error")
	}
}

func TestBuiltinResolvesAgainstItsOwnIds(t *testing.T) {
	// Every id a preset names has to exist somewhere, or the preset silently
	// shrinks. Collect the union and check nothing is dropped.
	defs := Builtin()
	seen := map[string]bool{}
	var order []string
	for _, d := range defs {
		for _, p := range d.Packages {
			if !seen[p] {
				seen[p] = true
				order = append(order, p)
			}
		}
	}
	got, err := Resolve(defs, order)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range defs {
		if d.All {
			continue
		}
		for _, p := range d.Packages {
			if !contains(got[i].Packages, p) {
				t.Errorf("%s lost %s", d.ID, p)
			}
		}
	}
}

func TestRemoteSupersedesBuiltin(t *testing.T) {
	body := `{"schema":1,"variants":[{"id":"only","name":"Only","packages":["gmscore"]}]}`
	defs, origin := Load(context.Background(), stub{body: body})
	if origin != OriginRemote {
		t.Errorf("origin = %q", origin)
	}
	if len(defs) != 1 || defs[0].ID != "only" {
		t.Errorf("got %+v", defs)
	}
}

// Every way the remote copy can be wrong has to leave the picker with the
// presets the binary shipped, rather than with none.
func TestBadRemoteFallsBackToBuiltin(t *testing.T) {
	builtinCount := len(Builtin())
	for name, s := range map[string]stub{
		"unreachable":    {err: errors.New("no")},
		"not json":       {body: "<html>404</html>"},
		"future schema":  {body: `{"schema":99,"variants":[{"id":"x","name":"X"}]}`},
		"no variants":    {body: `{"schema":1,"variants":[]}`},
		"empty document": {body: `{}`},
	} {
		defs, origin := Load(context.Background(), s)
		if origin != OriginBuiltin {
			t.Errorf("%s: origin = %q", name, origin)
		}
		if len(defs) != builtinCount {
			t.Errorf("%s: got %d presets, want the built-in %d", name, len(defs), builtinCount)
		}
	}
}

func TestNoSourceUsesBuiltin(t *testing.T) {
	defs, origin := Load(context.Background(), nil)
	if origin != OriginBuiltin || len(defs) == 0 {
		t.Errorf("origin %q, %d presets", origin, len(defs))
	}
}

type target struct {
	order []string
	got   []manifest.Variant
}

func (t *target) Order() []string                  { return t.order }
func (t *target) SetVariants(v []manifest.Variant) { t.got = v }

func TestApplyKeepsBuiltinWhenTheRemoteWillNotResolve(t *testing.T) {
	tg := &target{order: []string{"gmscore", "vending"}}
	body := `{"schema":1,"variants":[{"id":"a","name":"A","includes":"nope"}]}`
	if origin := Apply(context.Background(), stub{body: body}, tg); origin != OriginBuiltin {
		t.Errorf("origin = %q", origin)
	}
	if len(tg.got) == 0 {
		t.Error("the catalog was left with no presets")
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

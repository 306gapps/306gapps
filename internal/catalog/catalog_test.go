package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/306gapps/306gapps/internal/manifest"
)

func pkg(id string, opts ...func(*manifest.Package)) manifest.Package {
	p := manifest.Package{
		ID: id, Name: id, Group: "apps",
		Files: []manifest.File{{
			Path: "product/app/" + id + "/" + id + ".apk", Asset: id + ".apk",
			SHA256: strings.Repeat("a", 64), Size: 100, Kind: manifest.KindAPK,
		}},
	}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func requires(ids ...string) func(*manifest.Package) {
	return func(p *manifest.Package) { p.Requires = ids }
}
func conflicts(ids ...string) func(*manifest.Package) {
	return func(p *manifest.Package) { p.Conflicts = ids }
}
func required(p *manifest.Package) { p.Required = true }

func cat(pkgs ...manifest.Package) *Catalog {
	groups := map[string]bool{}
	var gs []manifest.Group
	for _, p := range pkgs {
		if !groups[p.Group] {
			groups[p.Group] = true
			gs = append(gs, manifest.Group{ID: p.Group, Name: p.Group})
		}
	}
	return New(&manifest.Manifest{
		Schema:   manifest.Schema,
		Release:  manifest.Release{ID: "test", Android: manifest.Android{API: 36}},
		Groups:   gs,
		Packages: pkgs,
	})
}

func ids(r *Resolution) []string {
	out := make([]string, len(r.Packages))
	for i, p := range r.Packages {
		out[i] = p.ID
	}
	return out
}

func TestResolvePullsTransitiveDeps(t *testing.T) {
	c := cat(pkg("gmscore"), pkg("vending", requires("gmscore")), pkg("setupwizard", requires("vending")))
	r, err := c.Resolve([]string{"setupwizard"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(r), ",")
	if got != "gmscore,vending,setupwizard" {
		t.Fatalf("got %q, want dependency order gmscore,vending,setupwizard", got)
	}
	if r.Implied["gmscore"] == nil || r.Implied["vending"] == nil {
		t.Fatalf("expected gmscore and vending marked implied, got %v", r.Implied)
	}
}

func TestResolveAlwaysIncludesRequired(t *testing.T) {
	c := cat(pkg("gmscore", required), pkg("calendar"))
	r, err := c.Resolve([]string{"calendar"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Selected("gmscore") {
		t.Fatalf("required package dropped: %v", ids(r))
	}
}

func TestResolveRejectsConflicts(t *testing.T) {
	c := cat(pkg("dialer-google"), pkg("dialer-aosp", conflicts("dialer-google")))
	_, err := c.Resolve([]string{"dialer-google", "dialer-aosp"})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if len(ce.Pairs) != 1 || ce.Pairs[0] != [2]string{"dialer-aosp", "dialer-google"} {
		t.Fatalf("unexpected pairs: %v", ce.Pairs)
	}
}

func TestResolveRejectsConflictPulledInByDependency(t *testing.T) {
	c := cat(
		pkg("dialer-aosp", conflicts("dialer-google")),
		pkg("dialer-google"),
		pkg("assistant", requires("dialer-google")),
	)
	if _, err := c.Resolve([]string{"dialer-aosp", "assistant"}); err == nil {
		t.Fatal("expected conflict via transitive dependency")
	}
}

func TestResolveUnknown(t *testing.T) {
	_, err := cat(pkg("gmscore")).Resolve([]string{"nope", "alsonope"})
	var ue *UnknownError
	if !errors.As(err, &ue) {
		t.Fatalf("want UnknownError, got %v", err)
	}
	if strings.Join(ue.IDs, ",") != "alsonope,nope" {
		t.Fatalf("unexpected ids %v", ue.IDs)
	}
}

func TestResolveDetectsCycle(t *testing.T) {
	c := cat(pkg("a", requires("b")), pkg("b", requires("a")))
	if _, err := c.Resolve([]string{"a"}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	c := cat(pkg("gmscore"), pkg("a", requires("gmscore")), pkg("b", requires("gmscore")), pkg("c"))
	first, err := c.Resolve([]string{"c", "b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Resolve([]string{"a", "c", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids(first), ",") != strings.Join(ids(second), ",") {
		t.Fatalf("order not stable: %v vs %v", ids(first), ids(second))
	}
}

func TestResolveDeduplicatesFilesAndSums(t *testing.T) {
	shared := manifest.File{Path: "product/etc/permissions/g.xml", Asset: "g.xml",
		SHA256: strings.Repeat("b", 64), Size: 50, Kind: manifest.KindPermission}
	a := pkg("a")
	a.Files = append(a.Files, shared)
	b := pkg("b")
	b.Files = append(b.Files, shared)
	r, err := cat(a, b).Resolve([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 3 {
		t.Fatalf("want 3 unique files, got %d", len(r.Files))
	}
	if r.Size != 250 {
		t.Fatalf("want size 250, got %d", r.Size)
	}
}

func TestDependents(t *testing.T) {
	c := cat(pkg("gmscore"), pkg("vending", requires("gmscore")), pkg("gsa", requires("gmscore")))
	got := strings.Join(c.Dependents("gmscore"), ",")
	if got != "gsa,vending" {
		t.Fatalf("got %q", got)
	}
}

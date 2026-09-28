// Command 306gapps builds custom Google apps packages for custom ROMs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/306gapps/306gapps/internal/build"
	"github.com/306gapps/306gapps/internal/catalog"
	"github.com/306gapps/306gapps/internal/manifest"
	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/stage"
	"github.com/306gapps/306gapps/internal/tui"
)

// DefaultSource is the assets repo; -source points at a fork or local mirror.
const DefaultSource = "https://raw.githubusercontent.com/306gapps/306gapps-assets/main"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "306gapps: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := "pick"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "pick":
		return cmdPick(ctx, args)
	case "list":
		return cmdList(ctx, args)
	case "build":
		return cmdBuild(ctx, args)
	case "uninstaller":
		return cmdUninstaller(ctx, args)
	case "cache":
		return cmdCache(args)
	case "validate":
		return cmdValidate(args)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `306gapps - build custom Google apps packages for custom ROMs

usage:
  306gapps [pick]                  interactive picker (default)
  306gapps list                    list available releases
  306gapps list <release>          list packages in a release
  306gapps build [flags]           build without the picker
  306gapps uninstaller [flags]     build a zip that removes an install
  306gapps cache [info|clear]      inspect or empty the download cache
  306gapps validate <manifest>     check a manifest for consistency

common flags:
  -source <url|dir>   assets repo or local mirror
  -cache <dir>        download cache location
`)
}

func commonFlags(fs *flag.FlagSet) (*string, *string) {
	src := fs.String("source", envOr("GAPPS_SOURCE", DefaultSource), "assets repo URL or local directory")
	cache := fs.String("cache", envOr("GAPPS_CACHE", ""), "download cache directory")
	return src, cache
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newSource(root, cache string) *source.Source {
	return source.New(root, source.NewCache(cache))
}

// resolveRelease finds a release by ID, by "android:<version>", or the newest one.
func resolveRelease(idx *source.Index, spec string) (source.ReleaseRef, error) {
	if spec == "" || spec == "latest" {
		ref, ok := idx.Latest(0)
		if !ok {
			return source.ReleaseRef{}, errors.New("index contains no releases")
		}
		return ref, nil
	}
	for _, r := range idx.Releases {
		if r.ID == spec {
			return r, nil
		}
	}
	// Accept a bare Android version, e.g. "16".
	for _, r := range idx.Releases {
		if r.Android.Version == spec {
			if ref, ok := idx.Latest(r.Android.API); ok {
				return ref, nil
			}
		}
	}
	var have []string
	for _, r := range idx.Releases {
		have = append(have, r.ID)
	}
	return source.ReleaseRef{}, fmt.Errorf("no release %q (have: %s)", spec, strings.Join(have, ", "))
}

func cmdPick(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pick", flag.ContinueOnError)
	src, cache := commonFlags(fs)
	out := fs.String("out", ".", "directory to write the built package to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	return tui.Run(ctx, newSource(*src, *cache), *out)
}

func cmdList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	src, cache := commonFlags(fs)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := newSource(*src, *cache)
	idx, err := s.Index(ctx)
	if err != nil {
		return err
	}

	if fs.NArg() == 0 {
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(idx.Releases)
		}
		if len(idx.Releases) == 0 {
			fmt.Fprintln(os.Stderr,
				"this source has published no releases yet")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RELEASE\tANDROID\tDEVICE\tBUILD\tPUBLISHED")
		refs := append([]source.ReleaseRef(nil), idx.Releases...)
		sort.Slice(refs, func(i, j int) bool { return refs[i].Created.After(refs[j].Created) })
		for _, r := range refs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				r.ID, r.Android.Version, r.Device, r.Build, r.Created.Format("2006-01-02"))
		}
		return w.Flush()
	}

	ref, err := resolveRelease(idx, fs.Arg(0))
	if err != nil {
		return err
	}
	m, err := s.Manifest(ctx, ref)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(m.Packages)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tCATEGORY\tSIZE\tFLAGS\tNAME")
	for _, p := range m.Packages {
		var flags []string
		if p.Required {
			flags = append(flags, "required")
		}
		if p.Default {
			flags = append(flags, "default")
		}
		if len(p.Requires) > 0 {
			flags = append(flags, "needs:"+strings.Join(p.Requires, "+"))
		}
		if len(p.Conflicts) > 0 {
			flags = append(flags, "conflicts:"+strings.Join(p.Conflicts, "+"))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			p.ID, p.Category, human(p.Size()), strings.Join(flags, ","), p.Name)
	}
	return w.Flush()
}

func cmdBuild(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	src, cache := commonFlags(fs)
	release := fs.String("release", "latest", "release ID, Android version, or \"latest\"")
	pkgs := fs.String("packages", "", "comma-separated package IDs (default: the release's defaults)")
	targetName := fs.String("target", string(build.TargetRecovery),
		"package format: "+strings.Join(targetNames(), ", "))
	out := fs.String("out", "", "output zip path (default: ./306gapps-<release>-<target>.zip)")
	workers := fs.Int("workers", 4, "concurrent downloads")
	// OTA-only inputs.
	base := fs.String("ota-base", "", "ota: your ROM's target-files zip (required)")
	keys := fs.String("ota-keys", "", "ota: directory holding your ROM's release keys")
	pkgKey := fs.String("ota-package-key", "", "ota: key signing the OTA, without extension (default <ota-keys>/releasekey)")
	tools := fs.String("ota-tools", "", "ota: AOSP otatools bin directory (default: PATH)")
	grow := fs.Bool("ota-grow", false, "ota: raise a partition's size budget if the selection overflows it")
	noBusybox := fs.Bool("no-busybox", false,
		"recovery: do not bundle busybox, use the recovery's own tools")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, err := build.ParseTarget(*targetName)
	if err != nil {
		return err
	}

	s := newSource(*src, *cache)
	idx, err := s.Index(ctx)
	if err != nil {
		return err
	}
	ref, err := resolveRelease(idx, *release)
	if err != nil {
		return err
	}
	m, err := s.Manifest(ctx, ref)
	if err != nil {
		return err
	}

	c := catalog.New(m)
	sel := c.Defaults()
	if *pkgs != "" {
		sel = strings.Split(*pkgs, ",")
		for i := range sel {
			sel[i] = strings.TrimSpace(sel[i])
		}
	}
	res, err := c.Resolve(sel)
	if err != nil {
		return err
	}

	if n := len(res.Implied); n > 0 {
		ids := make([]string, 0, n)
		for id := range res.Implied {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fmt.Fprintf(os.Stderr, "adding %d dependencies: %s\n", n, strings.Join(ids, ", "))
	}
	fmt.Fprintf(os.Stderr, "%s: %d packages, %s installed\n", ref.ID, len(res.Packages), human(res.Size))

	var lastPath string
	plan, err := stage.Build(ctx, s, m, res, stage.Options{
		Workers: *workers,
		Progress: func(f manifest.File, done, total int64) {
			if done >= total && f.Path != lastPath {
				lastPath = f.Path
				fmt.Fprintf(os.Stderr, "\r\033[Kfetched %s", f.Path)
			}
		},
	})
	if err != nil {
		return err
	}
	// Digests only prove the bytes match what was published; opening the archives
	// catches a source that published corrupt bytes.
	if err := plan.Verify(*workers); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\r\033[Kall payloads verified")

	// Recovery environments vary wildly; a bundled busybox makes one known toolset.
	busybox := ""
	if target == build.TargetRecovery && !*noBusybox {
		arch := m.Release.Architecture()
		if p, ok := idx.BusyboxFor(arch); ok {
			busybox, err = s.FetchPayload(ctx, p)
			if err != nil {
				return fmt.Errorf("busybox for %s: %w", arch, err)
			}
			fmt.Fprintf(os.Stderr, "bundling busybox %s\n", p.Version)
		} else {
			fmt.Fprintf(os.Stderr,
				"note: the source publishes no busybox for %s; "+
					"the installer will use the recovery's own tools\n", arch)
		}
	}

	dest := *out
	if dest == "" {
		dest = fmt.Sprintf("306gapps-%s-%s.zip", ref.ID, target)
	}
	if dir := filepath.Dir(dest); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	result, err := build.Build(plan, build.Options{
		Target:  target,
		Out:     dest,
		Busybox: busybox,
		Signing: build.SigningOptions{
			Base:       *base,
			KeyDir:     *keys,
			PackageKey: *pkgKey,
			ToolsDir:   *tools,
			Grow:       *grow,
			Log: func(line string) {
				fmt.Fprintf(os.Stderr, "\r\033[K%s\n", line)
			},
		},
		Progress: func(done, total int) {
			fmt.Fprintf(os.Stderr, "\r\033[Kpacking %d/%d", done, total)
		},
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\r\033[K")

	fmt.Printf("%s\n", result.Path)
	fmt.Fprintf(os.Stderr, "%s · %d entries · sha256 %s\n",
		human(result.Size), result.Files, result.SHA256)
	if target == build.TargetOTA && *keys == "" {
		fmt.Fprintln(os.Stderr,
			"\nthis is a merged target-files package, not a flashable zip.\n"+
				"Sign it with your usual flow, or re-run with -ota-keys to have\n"+
				"306gapps drive add_img_to_target_files, sign_target_files_apks\n"+
				"and ota_from_target_files for you.")
	}
	return nil
}

func cmdUninstaller(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstaller", flag.ContinueOnError)
	src, cache := commonFlags(fs)
	out := fs.String("out", "306gapps-uninstaller.zip", "output zip path")
	noBusybox := fs.Bool("no-busybox", false, "do not bundle busybox")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// No payload here, so the source is consulted only for busybox; missing is fine.
	busybox := ""
	if !*noBusybox {
		s := newSource(*src, *cache)
		if idx, err := s.Index(ctx); err == nil {
			if p, ok := idx.BusyboxFor("arm64"); ok {
				if local, err := s.FetchPayload(ctx, p); err == nil {
					busybox = local
				}
			}
		}
		if busybox == "" {
			fmt.Fprintln(os.Stderr,
				"note: no busybox available; the uninstaller will use the recovery's own tools")
		}
	}

	res, err := build.BuildUninstaller(build.UninstallerOptions{
		Out: *out, Busybox: busybox,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", res.Path)
	fmt.Fprintf(os.Stderr, "%s · sha256 %s\n\n", human(res.Size), res.SHA256)
	fmt.Fprintln(os.Stderr,
		"Dirty-flash your ROM first: that restores the apps the install replaced.\n"+
			"Then flash this to take the Google apps back out.")
	return nil
}

func cmdCache(args []string) error {
	fs := flag.NewFlagSet("cache", flag.ContinueOnError)
	dir := fs.String("cache", envOr("GAPPS_CACHE", ""), "download cache directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := source.NewCache(*dir)

	switch fs.Arg(0) {
	case "", "info":
		size, err := c.Size()
		if err != nil {
			return err
		}
		fmt.Printf("%s\n%s\n", c.Dir, human(size))
		return nil
	case "clear":
		if err := c.Clear(); err != nil {
			return err
		}
		fmt.Println("cache cleared")
		return nil
	default:
		return fmt.Errorf("unknown cache subcommand %q (want info or clear)", fs.Arg(0))
	}
}

func cmdValidate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: 306gapps validate <manifest.json>")
	}
	failed := false
	for _, path := range args {
		m, err := manifest.LoadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
			continue
		}
		// A manifest that parses can still describe an unbuildable selection.
		c := catalog.New(m)
		if _, err := c.Resolve(c.Defaults()); err != nil {
			fmt.Fprintf(os.Stderr, "%s: default selection does not resolve: %v\n", path, err)
			failed = true
			continue
		}
		var total int64
		for _, p := range m.Packages {
			total += p.Size()
		}
		fmt.Printf("%s: ok - %s, %d packages, %s\n",
			path, m.Release.ID, len(m.Packages), human(total))
	}
	if failed {
		return errors.New("validation failed")
	}
	return nil
}

func targetNames() []string {
	out := make([]string, len(build.Targets))
	for i, t := range build.Targets {
		out[i] = string(t)
	}
	return out
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

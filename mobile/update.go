package mobile

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/306gapps/306gapps/internal/source"
	"github.com/306gapps/306gapps/internal/update"
)

// Repo is where the app looks for a newer version of itself.
const Repo = "306gapps/306gapps"

// APKSuffix names the release asset this app can install over itself.
const APKSuffix = "-android-arm64.apk"

type updateJSON struct {
	Version string `json:"version"`
	Page    string `json:"page"`
	APK     string `json:"apk"`
	Size    int64  `json:"size"`
}

// CheckUpdate returns the newer release as JSON, or "" when there is none.
//
// A build that is not a released tag never reports one, so a dev build does
// not offer to replace itself with an older release.
func CheckUpdate(current string) (string, error) {
	rel, newer, err := update.Check(context.Background(), Repo, current)
	if err != nil || !newer {
		return "", err
	}
	// Only offer it if this release actually carries an apk; the first few
	// releases did not.
	asset, ok := rel.Asset(APKSuffix)
	if !ok {
		return "", nil
	}
	return marshal(updateJSON{Version: rel.Tag, Page: rel.URL, APK: asset.URL, Size: asset.Size})
}

// DownloadUpdate writes the apk to dir and returns the path.
//
// Goes through the same fetcher the payloads use, so it retries a server error
// rather than failing the update on one bad response.
func DownloadUpdate(url, dir string, p Progress) (string, error) {
	if url == "" {
		return "", fmt.Errorf("no download to fetch")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// A partial file left by an interrupted download must never be handed to
	// the installer, so build it under a temporary name.
	out := filepath.Join(dir, "306gapps-update.apk")
	tmp := out + ".part"

	rc, size, err := source.New("", nil).Open(context.Background(), url)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	var got int64
	_, err = io.Copy(f, readerFunc(func(b []byte) (int, error) {
		n, err := rc.Read(b)
		got += int64(n)
		if p != nil && size > 0 {
			p.Update("Downloading…", float64(got)/float64(size))
		}
		return n, err
	}))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if size > 0 && got != size {
		os.Remove(tmp)
		return "", fmt.Errorf("download stopped at %d of %d bytes", got, size)
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

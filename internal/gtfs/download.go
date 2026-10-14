package gtfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download saves the GTFS zip at url to path and returns its size. It
// writes to a temp file and renames, so a failed download never leaves a
// half-written zip where the service expects a good one.
func Download(ctx context.Context, url, path string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "gtfs-*.zip")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	return n, os.Rename(tmp.Name(), path)
}

package officialcatalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func Write(dir string, report Report) (string, error) {
	if dir == "" || report.SchemaVersion != SchemaVersion || report.ReportID == "" || report.InputDigest == "" {
		return "", errors.New("official review report is invalid")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.New("official review report serialization failed")
	}
	defer zero(data)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("official review output directory could not be created")
	}
	// Chmod sets restrictive POSIX mode bits. On Windows it is not a DACL
	// editor; operators must use a private output directory ACL there.
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", errors.New("official review output ID could not be created")
	}
	name := "official-review-" + report.CreatedAt.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(id) + ".json"
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".official-review-*.tmp")
	if err != nil {
		return "", errors.New("official review temporary file could not be created")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", errors.New("official review file permissions could not be restricted")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", errors.New("official review report write failed")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", errors.New("official review report flush failed")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("official review report close failed")
	}
	if _, err := os.Stat(path); err == nil {
		return "", errors.New("official review output name collision")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", errors.New("official review report publish failed")
	}
	return path, nil
}

func NewReportTime() time.Time { return time.Now().UTC() }

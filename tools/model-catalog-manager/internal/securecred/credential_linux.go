//go:build linux

package securecred

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sub2api-model-catalog-manager/internal/config"
)

const maxCredentialBytes = 16 * 1024

func readOSCredential(c config.CredentialConfig) (Value, error) {
	name := c.SystemdCredentialName
	if name == "" || c.WindowsCredentialTarget != "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return Value{}, ErrUnsupportedSource
	}
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" || !filepath.IsAbs(dir) {
		return Value{}, errors.New("systemd credential directory is not injected")
	}
	path := filepath.Join(dir, name)
	file, err := os.Open(path)
	if err != nil {
		return Value{}, errors.New("systemd credential file is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCredentialBytes {
		return Value{}, errors.New("systemd credential file has an invalid shape")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxCredentialBytes {
		zero(data)
		return Value{}, errors.New("systemd credential file could not be read safely")
	}
	defer zero(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw struct {
		APIKey string `json:"api_key"`
	}
	if err := dec.Decode(&raw); err != nil {
		return Value{}, errors.New("systemd credential file is not a valid credential object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF || raw.APIKey == "" {
		return Value{}, errors.New("systemd credential file is incomplete or contains trailing data")
	}
	apiKey := []byte(raw.APIKey)
	if !validAPIKey(apiKey) {
		zero(apiKey)
		return Value{}, errors.New("systemd admin API key is invalid")
	}
	return Value{APIKey: apiKey}, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

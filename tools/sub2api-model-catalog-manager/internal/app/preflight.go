package app

import (
	"errors"
	"os"

	"sub2api-model-catalog-manager/internal/state"
)

// optionalStateHash returns a stable sentinel for a state file that does not
// exist yet. This lets a read-only preflight bind the absent-file state and
// detect if another process creates it before the prepared operation begins.
func optionalStateHash(path string) (string, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", errors.New("state file status could not be read")
	}
	hash, err := state.Hash(path)
	if err != nil {
		return "", err
	}
	return hash, nil
}

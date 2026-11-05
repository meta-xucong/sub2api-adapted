//go:build !windows && !linux

package securecred

import "sub2api-model-catalog-manager/internal/config"

func readOSCredential(config.CredentialConfig) (Value, error) {
	return Value{}, ErrUnsupportedSource
}

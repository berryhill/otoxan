//go:build xander

package xander

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
)

//go:embed soul_default.txt
var embeddedDefault []byte

// ErrSoulNotFound is returned when neither the soul file nor the embedded default is available.
var ErrSoulNotFound = errors.New("soul: no persona file found and no embedded default available")

// LoadSOUL loads the persona file from path. If the file does not exist, it falls back
// to the embedded default SOUL. Returns the content and its SHA-256 hash.
func LoadSOUL(path string) ([]byte, string, error) {
	var data []byte
	var err error

	// Try loading from the filesystem first.
	data, err = os.ReadFile(path)
	if err == nil {
		// File exists and was read successfully.
		hash := sha256.Sum256(data)
		return data, hex.EncodeToString(hash[:]), nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		// File exists but couldn't be read (permission error, etc.).
		return nil, "", err
	}

	// File does not exist — fall back to embedded default.
	if len(embeddedDefault) == 0 {
		return nil, "", ErrSoulNotFound
	}

	hash := sha256.Sum256(embeddedDefault)
	return embeddedDefault, hex.EncodeToString(hash[:]), nil
}

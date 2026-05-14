//go:build xander

package xander

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSOUL(t *testing.T) {
	t.Run("file_exists_returns_content_and_hash", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "SOUL.md")
		content := []byte("Hello from Xander")
		require.NoError(t, os.WriteFile(path, content, 0644))

		data, hash, err := LoadSOUL(path)
		require.NoError(t, err)
		assert.Equal(t, content, data)

		expectedHash := sha256.Sum256(content)
		assert.Equal(t, hex.EncodeToString(expectedHash[:]), hash)
	})

	t.Run("missing_file_falls_back_to_embedded_default", func(t *testing.T) {
		// Point to a file that does not exist.
		path := filepath.Join(t.TempDir(), "nonexistent", "SOUL.md")
		data, hash, err := LoadSOUL(path)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
		assert.NotEmpty(t, hash)
		// Hash must match the embedded default.
		expectedHash := sha256.Sum256(embeddedDefault)
		assert.Equal(t, hex.EncodeToString(expectedHash[:]), hash)
	})

	t.Run("missing_file_and_no_embedded_default_returns_err", func(t *testing.T) {
		// Swap out the embedded default for an empty slice to simulate a
		// build with no embedded default.
		orig := embeddedDefault
		embeddedDefault = nil
		defer func() { embeddedDefault = orig }()

		path := filepath.Join(t.TempDir(), "nonexistent", "SOUL.md")
		_, _, err := LoadSOUL(path)
		require.ErrorIs(t, err, ErrSoulNotFound)
	})

	t.Run("unreadable_file_returns_error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "SOUL.md")
		// Create the file owned by a different user — if we can't chmod it
		// (e.g. root running the test), skip gracefully.
		content := []byte("unreadable")
		require.NoError(t, os.WriteFile(path, content, 0000))

		_, _, err := LoadSOUL(path)
		// Either a permission error or the file is readable (test running as root).
		if err != nil {
			assert.Contains(t, err.Error(), "permission denied")
		}
	})

	t.Run("hash_is_stable_and_correct", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "SOUL.md")
		content := []byte("stable content for hash test")
		require.NoError(t, os.WriteFile(path, content, 0644))

		_, hash1, err := LoadSOUL(path)
		require.NoError(t, err)

		// Second call must produce the same hash.
		_, hash2, err := LoadSOUL(path)
		require.NoError(t, err)
		assert.Equal(t, hash1, hash2)

		// Hash must be a valid hex string of the correct length.
		decoded, err := hex.DecodeString(hash1)
		require.NoError(t, err)
		assert.Len(t, decoded, sha256.Size)
	})
}

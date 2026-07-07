package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	colorRed = ""
	colorGreen = ""
	colorYellow = ""
	colorDim = ""
	colorBold = ""
	colorReset = ""
	os.Exit(m.Run())
}

func TestResolveFiles(t *testing.T) {
	t.Run("explicit files returned as-is", func(t *testing.T) {
		files, err := resolveFiles([]string{"a.yml", "b.yml"}, ".")
		require.NoError(t, err)
		assert.Equal(t, []string{"a.yml", "b.yml"}, files)
	})

	t.Run("discovers files from directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))

		files, err := resolveFiles(nil, dir)
		require.NoError(t, err)
		assert.Len(t, files, 1)
	})

	t.Run("empty directory returns nil", func(t *testing.T) {
		files, err := resolveFiles(nil, t.TempDir())
		require.NoError(t, err)
		assert.Nil(t, files)
	})

	t.Run("invalid directory returns error", func(t *testing.T) {
		_, err := resolveFiles(nil, filepath.Join(t.TempDir(), "nonexistent"))
		assert.Error(t, err)
	})
}

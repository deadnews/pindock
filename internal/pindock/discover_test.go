package pindock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyFile(t *testing.T) {
	tests := []struct {
		path   string
		wantFT FileType
		wantOK bool
	}{
		{"Dockerfile", FileTypeDockerfile, true},
		{"dockerfile", FileTypeDockerfile, true},
		{"Dockerfile.dev", FileTypeDockerfile, true},
		{"my.dockerfile", FileTypeDockerfile, true},
		{"Containerfile", FileTypeDockerfile, true},
		{"Containerfile.prod", FileTypeDockerfile, true},
		{"my.containerfile", FileTypeDockerfile, true},
		{"path/to/Dockerfile", FileTypeDockerfile, true},

		{"compose.yml", FileTypeCompose, true},
		{"compose.yaml", FileTypeCompose, true},
		{"compose.dev.yml", FileTypeCompose, true},
		{"docker-compose.yml", FileTypeCompose, true},
		{"docker-compose.prod.yaml", FileTypeCompose, true},
		{"path/to/compose.yml", FileTypeCompose, true},

		{"README.md", 0, false},
		{"main.go", 0, false},
		{"compose.toml", 0, false},
		{"dockerfile_test.go", 0, false},
		{"dockerfile.py", 0, false},
		{"dockerfile_parser.rs", 0, false},
		{"dockerfile-builder.js", 0, false},
		{"dockerfile.test.go", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ft, ok := ClassifyFile(tt.path)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantFT, ft)
			}
		})
	}
}

func TestDiscoverFiles(t *testing.T) {
	t.Run("basic filtering", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(""), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte(""), 0o600))

		files, err := DiscoverFiles(dir)
		require.NoError(t, err)
		assert.Contains(t, files, filepath.Join(dir, "Dockerfile"))
		assert.Contains(t, files, filepath.Join(dir, "compose.yml"))
		assert.NotContains(t, files, filepath.Join(dir, "README.md"))
	})

	t.Run("nonexistent root", func(t *testing.T) {
		_, err := DiscoverFiles(filepath.Join(t.TempDir(), "nonexistent"))
		assert.Error(t, err)
	})

	t.Run("empty directory", func(t *testing.T) {
		files, err := DiscoverFiles(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, files)
	})

	t.Run("subdirectories and dotfile skipping", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "services", "web"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "services", "web", "Dockerfile"), []byte("FROM scratch\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "services", "compose.yml"), []byte(""), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "Dockerfile"), []byte("FROM scratch\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte(""), 0o600))

		files, err := DiscoverFiles(dir)
		require.NoError(t, err)
		assert.Contains(t, files, filepath.Join(dir, "Dockerfile"))
		assert.Contains(t, files, filepath.Join(dir, "services", "web", "Dockerfile"))
		assert.Contains(t, files, filepath.Join(dir, "services", "compose.yml"))
		assert.NotContains(t, files, filepath.Join(dir, ".git", "Dockerfile"))
		assert.NotContains(t, files, filepath.Join(dir, "README.md"))
	})
}

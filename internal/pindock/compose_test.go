package pindock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCompose(t *testing.T) {
	t.Run("simple image", func(t *testing.T) {
		content := `services:
  db:
    image: postgres:18-alpine`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
		assert.Empty(t, refs[0].Digest)
	})

	t.Run("image with digest", func(t *testing.T) {
		content := `services:
  db:
    image: postgres:18-alpine@sha256:abc123`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
		assert.Equal(t, "sha256:abc123", refs[0].Digest)
	})

	t.Run("multiple services", func(t *testing.T) {
		content := `services:
  web:
    image: nginx:1.27
  db:
    image: postgres:18-alpine
  cache:
    image: redis:7-alpine`
		refs := ParseCompose(content)
		require.Len(t, refs, 3)
		assert.Equal(t, "nginx:1.27", refs[0].TagRef)
		assert.Equal(t, "postgres:18-alpine", refs[1].TagRef)
		assert.Equal(t, "redis:7-alpine", refs[2].TagRef)
	})

	t.Run("image with registry prefix", func(t *testing.T) {
		content := `services:
  app:
    image: ghcr.io/org/app:latest`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "ghcr.io/org/app:latest", refs[0].TagRef)
	})

	t.Run("image with quotes", func(t *testing.T) {
		content := `services:
  db:
    image: "postgres:18-alpine"`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
	})

	t.Run("image with single quotes", func(t *testing.T) {
		content := `services:
  db:
    image: 'postgres:18-alpine'`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
	})

	t.Run("service with build instead of image skipped", func(t *testing.T) {
		content := `services:
  app:
    build: .
  db:
    image: postgres:18-alpine`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
	})

	t.Run("variable image skipped", func(t *testing.T) {
		content := `services:
  db:
    image: ${DB_IMAGE}`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.True(t, refs[0].HasVariable())
	})

	t.Run("image with comment", func(t *testing.T) {
		content := `services:
  db:
    image: postgres:18-alpine # database`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "postgres:18-alpine", refs[0].TagRef)
	})

	t.Run("YAML anchor with image", func(t *testing.T) {
		content := `x-shared: &shared
  image: debian:11
services:
  a:
    <<: *shared
  b:
    <<: *shared`
		refs := ParseCompose(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "debian:11", refs[0].TagRef)
	})
}

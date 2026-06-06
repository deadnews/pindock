package registry

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startRegistry serves an in-memory registry and returns its host:port.
func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pushTag writes a random image to repo:tag and returns its digest.
func pushTag(t *testing.T, repo, tag string) string {
	t.Helper()
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	ref, err := name.NewTag(repo + ":" + tag)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img))
	digest, err := img.Digest()
	require.NoError(t, err)
	return digest.String()
}

func TestSimplifyError(t *testing.T) {
	t.Run("transport error", func(t *testing.T) {
		err := &transport.Error{StatusCode: 404}
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "404 not-found")
	})

	t.Run("transport error unauthorized", func(t *testing.T) {
		err := &transport.Error{StatusCode: 401}
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "401 unauthorized")
	})

	t.Run("non-transport error passed through", func(t *testing.T) {
		err := errors.New("connection refused")
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "connection refused")
	})
}

func TestResolveDigest(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	want := pushTag(t, repo, "1.0")

	got, err := ResolveDigest(t.Context(), repo+":1.0")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestResolveDigest_notFound(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	_, err := ResolveDigest(t.Context(), repo+":missing")
	assert.Error(t, err)
}

func TestResolveAll(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	d1 := pushTag(t, repo, "1.0")
	d2 := pushTag(t, repo, "2.0")
	ref1, ref2 := repo+":1.0", repo+":2.0"

	digests, errs := ResolveAll(t.Context(), []string{ref1, ref2, ref1})
	assert.Empty(t, errs)
	assert.Equal(t, d1, digests[ref1])
	assert.Equal(t, d2, digests[ref2])
}

func TestResolveAll_partialError(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "1.0")
	good, bad := repo+":1.0", repo+":missing"

	digests, errs := ResolveAll(t.Context(), []string{good, bad})
	assert.Contains(t, digests, good)
	assert.Contains(t, errs, bad)
}

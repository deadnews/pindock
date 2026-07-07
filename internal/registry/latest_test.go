package registry

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLatestCandidates(t *testing.T) {
	tags := []string{"latest", "8", "8.0", "8.0.1", "8.0.1-bookworm", "bookworm", "9.0.0", "v8.0.1", "7.9.9", "resolute-20260627"}
	got := latestCandidates(tags)
	assert.Equal(t, []string{"9.0.0", "8.0.1", "8.0", "8", "7.9.9", "resolute-20260627", "v8.0.1"}, got)
}

func TestReplaceTag(t *testing.T) {
	assert.Equal(t, "redis:8", replaceTag("redis:latest", "8"))
	assert.Equal(t, "redis:8", replaceTag("redis", "8"))
	assert.Equal(t, "myregistry:5000/img:8", replaceTag("myregistry:5000/img", "8"))
	assert.Equal(t, "myregistry:5000/img:8", replaceTag("myregistry:5000/img:latest", "8"))
}

func TestResolveLatestTags(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	pushTag(t, repo, "latest", "8", "8.0", "8.0.1", "8.0.1-bookworm")
	pushTag(t, repo, "9.0.0") // newer but not what latest points at

	t.Run("picks most specific matching alias", func(t *testing.T) {
		ref := repo + ":latest"
		updates, failed := ResolveLatestTags(t.Context(), []string{ref})
		assert.Empty(t, failed)
		assert.Equal(t, repo+":8.0.1", updates[ref])
	})

	t.Run("untagged ref", func(t *testing.T) {
		updates, failed := ResolveLatestTags(t.Context(), []string{repo})
		assert.Empty(t, failed)
		assert.Equal(t, repo+":8.0.1", updates[repo])
	})

	t.Run("non-latest refs ignored", func(t *testing.T) {
		updates, failed := ResolveLatestTags(t.Context(), []string{repo + ":8"})
		assert.Empty(t, failed)
		assert.Empty(t, updates)
	})
}

func TestResolveLatestTags_noVersionedAlias(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "latest", "bookworm")

	updates, failed := ResolveLatestTags(t.Context(), []string{repo + ":latest"})
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

func TestResolveLatestTags_missingLatest(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "1.0")

	updates, failed := ResolveLatestTags(t.Context(), []string{repo + ":latest"})
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

func TestResolveLatestTags_repoError(t *testing.T) {
	host := startRegistry(t)
	ref := host + "/lib/missing:latest"

	updates, failed := ResolveLatestTags(t.Context(), []string{ref})
	assert.Empty(t, updates)
	assert.Contains(t, failed, ref)
}

func TestResolveLatestTags_candidateCap(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "latest", "1.0")
	for i := range maxLatestCandidates {
		pushTag(t, repo, fmt.Sprintf("%d.0", i+2))
	}

	updates, failed := ResolveLatestTags(t.Context(), []string{repo + ":latest"})
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

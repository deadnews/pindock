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

func TestFindTagUpdates_latestAlias(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	pushTag(t, repo, "latest", "8", "8.0", "8.0.1", "8.0.1-bookworm")
	pushTag(t, repo, "9.0.0") // newer but not what latest points at

	t.Run("picks most specific matching alias", func(t *testing.T) {
		ref := repo + ":latest"
		updates, held, failed := FindTagUpdates(t.Context(), []string{ref}, false)
		assert.Empty(t, failed)
		assert.Empty(t, held)
		assert.Equal(t, repo+":8.0.1", updates[ref])
	})

	t.Run("untagged ref", func(t *testing.T) {
		updates, _, failed := FindTagUpdates(t.Context(), []string{repo}, false)
		assert.Empty(t, failed)
		assert.Equal(t, repo+":8.0.1", updates[repo])
	})

	t.Run("versioned refs ignored without versioned", func(t *testing.T) {
		updates, _, failed := FindTagUpdates(t.Context(), []string{repo + ":8"}, false)
		assert.Empty(t, failed)
		assert.Empty(t, updates)
	})

	t.Run("latest alias and held update resolve in one pass", func(t *testing.T) {
		latestRef, versionedRef := repo+":latest", repo+":8.0.1"
		updates, held, failed := FindTagUpdates(t.Context(), []string{latestRef, versionedRef}, true)
		assert.Empty(t, failed)
		assert.Equal(t, repo+":8.0.1", updates[latestRef])
		assert.Equal(t, repo+":9.0.0", held[versionedRef])
	})
}

func TestFindTagUpdates_noVersionedAlias(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "latest", "bookworm")

	updates, _, failed := FindTagUpdates(t.Context(), []string{repo + ":latest"}, false)
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

func TestFindTagUpdates_missingLatest(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "1.0")

	updates, _, failed := FindTagUpdates(t.Context(), []string{repo + ":latest"}, false)
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

func TestFindTagUpdates_latestRepoError(t *testing.T) {
	host := startRegistry(t)
	ref := host + "/lib/missing:latest"

	updates, _, failed := FindTagUpdates(t.Context(), []string{ref}, false)
	assert.Empty(t, updates)
	assert.Contains(t, failed, ref)
}

func TestFindTagUpdates_candidateCap(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "latest", "1.0")
	for i := range maxLatestCandidates {
		pushTag(t, repo, fmt.Sprintf("%d.0", i+2))
	}

	updates, _, failed := FindTagUpdates(t.Context(), []string{repo + ":latest"}, false)
	assert.Empty(t, failed)
	assert.Empty(t, updates)
}

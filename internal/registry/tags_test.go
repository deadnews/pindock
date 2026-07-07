package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersionedTag(t *testing.T) {
	tests := []struct {
		tag     string
		wantPfx string
		wantVer []int
		wantSfx string
		wantOK  bool
	}{
		{"7-alpine", "", []int{7}, "-alpine", true},
		{"3.12-slim", "", []int{3, 12}, "-slim", true},
		{"1.26.0", "", []int{1, 26, 0}, "", true},
		{"8", "", []int{8}, "", true},
		{"3.12-slim-bookworm", "", []int{3, 12}, "-slim-bookworm", true},
		{"alpine-1.23.5", "alpine-", []int{1, 23, 5}, "", true},
		{"alpine-3.21", "alpine-", []int{3, 21}, "", true},
		{"v1.26.0", "v", []int{1, 26, 0}, "", true},
		{"0.0.1-alpha.3", "", []int{0, 0, 1, 3}, "-alpha.", true},
		{"1.2.3-rc.1", "", []int{1, 2, 3, 1}, "-rc.", true},
		{"1.2.3-beta4", "", []int{1, 2, 3, 4}, "-beta", true},
		{"1.2.3-debian-11", "", []int{1, 2, 3}, "-debian-11", true},
		{"latest", "", nil, "", false},
		{"alpine", "", nil, "", false},
		{"bookworm", "", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			parsed, ok := parseVersionedTag(tt.tag)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantPfx, parsed.prefix)
				assert.Equal(t, tt.wantVer, parsed.version)
				assert.Equal(t, tt.wantSfx, parsed.suffix)
			}
		})
	}
}

func TestFindLatestTag(t *testing.T) {
	allTags := []string{
		"5-alpine", "6-alpine", "7-alpine", "8-alpine",
		"7.4-alpine", "7.4.1-alpine", "8.0-alpine", "8.0.1-alpine",
		"7", "8", "7.4", "8.0",
		"alpine-1.23.3", "alpine-1.23.4", "alpine-1.23.5",
		"alpine-3.20", "alpine-3.21",
		"v1.25.0", "v1.26.0", "v2.0.0",
		"0.0.1-alpha.3", "0.0.1-alpha.4", "0.0.2-alpha.1", "0.0.2",
		"1.2.3-debian-11", "1.2.3-debian-12",
		"latest", "alpine", "bookworm",
	}

	tests := []struct {
		name    string
		current string
		want    string
		wantOK  bool
	}{
		{"major with suffix", "7-alpine", "8-alpine", true},
		{"major without suffix", "7", "8", true},
		{"major.minor with suffix", "7.4-alpine", "8.0-alpine", true},
		{"major.minor without suffix", "7.4", "8.0", true},
		{"major.minor.patch with suffix", "7.4.1-alpine", "8.0.1-alpine", true},
		{"already latest", "8-alpine", "", false},
		{"non-versioned", "alpine", "", false},
		{"no matching suffix", "7-bullseye", "", false},
		{"prefix alpine semver", "alpine-1.23.3", "alpine-1.23.5", true},
		{"prefix alpine major.minor", "alpine-3.20", "alpine-3.21", true},
		{"prefix alpine already latest", "alpine-1.23.5", "", false},
		{"prefix v semver", "v1.26.0", "v2.0.0", true},
		{"no matching prefix", "rel-1.23.3", "", false},
		{"prerelease newest in stream", "0.0.1-alpha.3", "0.0.2-alpha.1", true},
		{"stable ignores prerelease", "0.0.2", "", false},
		{"prerelease stays in stream", "0.0.2-alpha.1", "", false},
		{"distro suffix fixed", "1.2.3-debian-11", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findLatestTag(tt.current, allTags, nil)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseVersionedTag_prerelease(t *testing.T) {
	for tag, want := range map[string]bool{
		"1.2.3-rc.1":    true,
		"1.2.3-beta4":   true,
		"0.0.1-alpha.3": true,
		"1.2.3":         false,
		"7-alpine":      false,
	} {
		parsed, ok := parseVersionedTag(tag)
		assert.True(t, ok, tag)
		assert.Equal(t, want, parsed.prerelease, tag)
	}
}

func TestExceedsLimit(t *testing.T) {
	assert.False(t, exceedsLimit([]int{8, 0, 1}, nil))
	assert.False(t, exceedsLimit([]int{8, 0, 1}, []int{8, 0, 1}))
	assert.False(t, exceedsLimit([]int{8, 0, 1}, []int{8, 0}))
	assert.False(t, exceedsLimit([]int{8}, []int{8, 0, 1}))
	assert.True(t, exceedsLimit([]int{8, 1, 0}, []int{8, 0}))
	assert.True(t, exceedsLimit([]int{9}, []int{8, 0, 1}))
}

func TestFindLatestTag_limit(t *testing.T) {
	allTags := []string{"7.4-alpine", "7.5-alpine", "8.0-alpine", "0.0.1-alpha.3", "0.0.2-alpha.1"}

	t.Run("blocks candidates past limit", func(t *testing.T) {
		got, ok := findLatestTag("7.4-alpine", allTags, []int{7, 9})
		assert.True(t, ok)
		assert.Equal(t, "7.5-alpine", got)
	})

	t.Run("allows candidates at limit", func(t *testing.T) {
		got, ok := findLatestTag("7.4-alpine", allTags, []int{8, 0})
		assert.True(t, ok)
		assert.Equal(t, "8.0-alpine", got)
	})

	t.Run("no candidate within limit", func(t *testing.T) {
		_, ok := findLatestTag("7.5-alpine", allTags, []int{7, 5})
		assert.False(t, ok)
	})

	t.Run("prerelease stream ignores limit", func(t *testing.T) {
		got, ok := findLatestTag("0.0.1-alpha.3", allTags, []int{0, 0, 1})
		assert.True(t, ok)
		assert.Equal(t, "0.0.2-alpha.1", got)
	})
}

func TestFindLatestTags(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	for _, tag := range []string{"1.0", "1.1", "2.0", "latest"} {
		pushTag(t, repo, tag)
	}

	t.Run("upgrades to newest", func(t *testing.T) {
		ref := repo + ":1.0"
		updates, held, failed := FindLatestTags(t.Context(), []string{ref})
		assert.Empty(t, failed)
		assert.Empty(t, held)
		assert.Equal(t, repo+":2.0", updates[ref])
	})

	t.Run("already newest", func(t *testing.T) {
		updates, held, failed := FindLatestTags(t.Context(), []string{repo + ":2.0"})
		assert.Empty(t, failed)
		assert.Empty(t, held)
		assert.Empty(t, updates)
	})

	t.Run("latest tag ignored", func(t *testing.T) {
		updates, held, failed := FindLatestTags(t.Context(), []string{repo + ":latest"})
		assert.Empty(t, failed)
		assert.Empty(t, held)
		assert.Empty(t, updates)
	})
}

func TestFindLatestTags_cappedByLatest(t *testing.T) {
	repo := startRegistry(t) + "/lib/app"
	pushTag(t, repo, "1.1", "latest") // latest marks 1.1 as stable
	pushTag(t, repo, "1.0")
	pushTag(t, repo, "2.0") // unmarked prerelease

	t.Run("stops at latest version", func(t *testing.T) {
		ref := repo + ":1.0"
		updates, held, failed := FindLatestTags(t.Context(), []string{ref})
		assert.Empty(t, failed)
		assert.Empty(t, held)
		assert.Equal(t, repo+":1.1", updates[ref])
	})

	t.Run("no update past latest is reported held", func(t *testing.T) {
		updates, held, failed := FindLatestTags(t.Context(), []string{repo + ":1.1"})
		assert.Empty(t, failed)
		assert.Empty(t, updates)
		assert.Equal(t, repo+":2.0", held[repo+":1.1"])
	})
}

func TestFindLatestTags_repoError(t *testing.T) {
	host := startRegistry(t)
	ref := host + "/lib/missing:1.0"
	updates, held, failed := FindLatestTags(t.Context(), []string{ref})
	assert.Empty(t, updates)
	assert.Empty(t, held)
	assert.Contains(t, failed, ref)
}

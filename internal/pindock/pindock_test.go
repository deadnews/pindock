package pindock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		input      string
		wantTagRef string
		wantDigest string
	}{
		{
			input:      "golang:1.26-alpine",
			wantTagRef: "golang:1.26-alpine",
			wantDigest: "",
		},
		{
			input:      "golang:1.26-alpine@sha256:abc123",
			wantTagRef: "golang:1.26-alpine",
			wantDigest: "sha256:abc123",
		},
		{
			input:      "gcr.io/distroless/static@sha256:def456",
			wantTagRef: "gcr.io/distroless/static",
			wantDigest: "sha256:def456",
		},
		{
			input:      "ghcr.io/org/repo:1.0@sha256:789",
			wantTagRef: "ghcr.io/org/repo:1.0",
			wantDigest: "sha256:789",
		},
		{
			input:      "ubuntu",
			wantTagRef: "ubuntu",
			wantDigest: "",
		},
		{
			input:      "myregistry:5000/image:tag",
			wantTagRef: "myregistry:5000/image:tag",
			wantDigest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ref := ParseImageRef(tt.input)
			assert.Equal(t, tt.input, ref.Original)
			assert.Equal(t, tt.wantTagRef, ref.TagRef)
			assert.Equal(t, tt.wantDigest, ref.Digest)
		})
	}
}

func TestImageRef_NeedsUpdate(t *testing.T) {
	unpinned := ParseImageRef("golang:1.26-alpine")
	assert.True(t, unpinned.NeedsUpdate("sha256:abc"))

	current := ParseImageRef("golang:1.26-alpine@sha256:abc")
	assert.False(t, current.NeedsUpdate("sha256:abc"))

	stale := ParseImageRef("golang:1.26-alpine@sha256:old")
	assert.True(t, stale.NeedsUpdate("sha256:new"))
}

func TestImageRef_HasVariable(t *testing.T) {
	assert.False(t, ParseImageRef("golang:1.26").HasVariable())
	assert.True(t, ParseImageRef("golang:${TAG}").HasVariable())
	assert.True(t, ParseImageRef("${BASE_IMAGE}").HasVariable())
}

func TestApplyReplacements(t *testing.T) {
	t.Run("single replacement", func(t *testing.T) {
		content := "FROM golang:1.26-alpine AS builder"
		result := applyReplacements(content, []replacement{
			{start: 5, end: 23, newStr: "golang:1.26-alpine@sha256:abc"},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:abc AS builder", result)
	})

	t.Run("update digested ref", func(t *testing.T) {
		content := "FROM golang:1.26-alpine@sha256:old AS builder"
		result := applyReplacements(content, []replacement{
			{start: 5, end: 34, newStr: "golang:1.26-alpine@sha256:new"},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:new AS builder", result)
	})

	t.Run("multiple replacements same image", func(t *testing.T) {
		content := "FROM golang:1.26 AS a\nFROM golang:1.26 AS b"
		result := applyReplacements(content, []replacement{
			{start: 5, end: 16, newStr: "golang:1.26@sha256:abc"},
			{start: 27, end: 38, newStr: "golang:1.26@sha256:abc"},
		})
		assert.Equal(t, "FROM golang:1.26@sha256:abc AS a\nFROM golang:1.26@sha256:abc AS b", result)
	})

	t.Run("mixed digested and unpinned", func(t *testing.T) {
		content := "FROM golang:1.26-alpine@sha256:old AS a\nFROM golang:1.26-alpine AS b"
		result := applyReplacements(content, []replacement{
			{start: 5, end: 34, newStr: "golang:1.26-alpine@sha256:new"},
			{start: 45, end: 63, newStr: "golang:1.26-alpine@sha256:new"},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:new AS a\nFROM golang:1.26-alpine@sha256:new AS b", result)
	})

	t.Run("distinct refs at different offsets", func(t *testing.T) {
		content := "FROM nginx:1.27 AS web\nFROM golang:1.26-alpine AS builder"
		result := applyReplacements(content, []replacement{
			{start: 5, end: 15, newStr: "nginx:1.27@sha256:aaa"},
			{start: 28, end: 46, newStr: "golang:1.26-alpine@sha256:bbb"},
		})
		assert.Equal(t, "FROM nginx:1.27@sha256:aaa AS web\nFROM golang:1.26-alpine@sha256:bbb AS builder", result)
	})

	t.Run("does not corrupt comment containing image ref", func(t *testing.T) {
		content := "# Use nginx:1.27 for production\nFROM nginx:1.27"
		result := applyReplacements(content, []replacement{
			{start: 37, end: 47, newStr: "nginx:1.27@sha256:abc"},
		})
		assert.Equal(t, "# Use nginx:1.27 for production\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("does not corrupt ARG value containing image ref", func(t *testing.T) {
		content := "ARG IMAGE=nginx:1.27\nFROM nginx:1.27"
		result := applyReplacements(content, []replacement{
			{start: 26, end: 36, newStr: "nginx:1.27@sha256:abc"},
		})
		assert.Equal(t, "ARG IMAGE=nginx:1.27\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("does not corrupt substring in unrelated image", func(t *testing.T) {
		content := "ENV IMG=custom-nginx:1.27\nFROM nginx:1.27"
		result := applyReplacements(content, []replacement{
			{start: 31, end: 41, newStr: "nginx:1.27@sha256:abc"},
		})
		assert.Equal(t, "ENV IMG=custom-nginx:1.27\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("empty", func(t *testing.T) {
		content := "FROM golang:1.26"
		assert.Equal(t, content, applyReplacements(content, nil))
	})
}

func TestShouldSkip(t *testing.T) {
	assert.False(t, shouldSkip(ParseImageRef("golang:1.26")))
	assert.True(t, shouldSkip(ParseImageRef("scratch")))
	assert.True(t, shouldSkip(ParseImageRef("golang:${TAG}")))
}

func TestCollectResolvable(t *testing.T) {
	parsed := []fileData{
		{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("scratch")}},
		{refs: []ImageRef{ParseImageRef("${BASE}"), ParseImageRef("nginx:1.27")}},
	}

	t.Run("without update", func(t *testing.T) {
		refs := collectResolvable(parsed, false, nil)
		require.Len(t, refs, 2)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
		assert.Equal(t, "nginx:1.27", refs[1].TagRef)
	})

	t.Run("skips pinned without update", func(t *testing.T) {
		data := []fileData{
			{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27@sha256:abc")}},
		}
		refs := collectResolvable(data, false, nil)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})

	t.Run("includes pinned with update", func(t *testing.T) {
		data := []fileData{
			{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27@sha256:abc")}},
		}
		refs := collectResolvable(data, true, nil)
		require.Len(t, refs, 2)
	})
}

func TestClassifyRefs(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("scratch")}}
		results, repls := classifyRefs(fp, &resolveData{}, false, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
		assert.Empty(t, repls)
	})

	t.Run("pinned treated as current without update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:abc")}}
		results, repls := classifyRefs(fp, &resolveData{}, false, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
		assert.Empty(t, repls)
	})

	t.Run("current with update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:abc")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"golang:1.26": "sha256:abc"},
		}, false, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
		assert.Empty(t, repls)
	})

	t.Run("unpinned in fix mode", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"golang:1.26": "sha256:abc"},
		}, true, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		require.Len(t, repls, 1)
		assert.Equal(t, "golang:1.26@sha256:abc", repls[0].newStr)
	})

	t.Run("unpinned in check mode", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"golang:1.26": "sha256:abc"},
		}, false, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		assert.Empty(t, repls)
	})

	t.Run("outdated with update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"golang:1.26": "sha256:new"},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		require.Len(t, repls, 1)
		assert.Equal(t, "golang:1.26@sha256:new", repls[0].newStr)
	})

	t.Run("outdated without update treated as current", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{}, true, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
		assert.Empty(t, repls)
	})

	t.Run("resolve error", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("bad:ref")}}
		results, _ := classifyRefs(fp, &resolveData{
			errs: map[string]error{"bad:ref": errors.New("not found")},
		}, false, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.EqualError(t, results[0].Err, "not found")
	})

	t.Run("missing from both maps", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("unknown:ref")}}
		results, _ := classifyRefs(fp, &resolveData{
			digests: map[string]string{},
			errs:    map[string]error{},
		}, false, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.Contains(t, results[0].Err.Error(), "failed to resolve")
	})
}

func TestClassifyRefs_tagUpdate(t *testing.T) {
	t.Run("tag updated with fix", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests:    map[string]string{"redis:8-alpine": "sha256:new"},
			tagUpdates: map[string]string{"redis:7-alpine": "redis:8-alpine"},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Equal(t, "redis:8-alpine", results[0].NewTagRef)
		require.Len(t, repls, 1)
		assert.Equal(t, "redis:8-alpine@sha256:new", repls[0].newStr)
	})

	t.Run("tag updated unpinned ref", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests:    map[string]string{"redis:8-alpine": "sha256:new"},
			tagUpdates: map[string]string{"redis:7-alpine": "redis:8-alpine"},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		assert.Equal(t, "redis:8-alpine", results[0].NewTagRef)
		require.Len(t, repls, 1)
		assert.Equal(t, "redis:8-alpine@sha256:new", repls[0].newStr)
	})

	t.Run("no tag update falls through to digest", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests:    map[string]string{"redis:7-alpine": "sha256:new"},
			tagUpdates: map[string]string{},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Empty(t, results[0].NewTagRef)
		require.Len(t, repls, 1)
		assert.Equal(t, "redis:7-alpine@sha256:new", repls[0].newStr)
	})

	t.Run("tag listing error on pinned ref reports error", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests:   map[string]string{"redis:7-alpine": "sha256:new"},
			tagErrors: map[string]error{"redis:7-alpine": errors.New("auth failed")},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.Contains(t, results[0].Err.Error(), "auth failed")
		assert.Empty(t, repls)
	})

	t.Run("tag listing error on unpinned ref still pins", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests:   map[string]string{"redis:7-alpine": "sha256:abc"},
			tagErrors: map[string]error{"redis:7-alpine": errors.New("auth failed")},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		require.Len(t, repls, 1)
		assert.Equal(t, "redis:7-alpine@sha256:abc", repls[0].newStr)
	})
}

func TestCollectResolvable_tagUpdate(t *testing.T) {
	data := []fileData{
		{refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:abc")}},
	}
	tagUpdates := map[string]string{"redis:7-alpine": "redis:8-alpine"}
	refs := collectResolvable(data, true, tagUpdates)
	require.Len(t, refs, 1)
	assert.Equal(t, "redis:8-alpine", refs[0].TagRef)
}

func TestResult_PinnedRef(t *testing.T) {
	t.Run("without tag update", func(t *testing.T) {
		r := Result{Ref: ParseImageRef("golang:1.26"), NewDigest: "sha256:abc"}
		assert.Equal(t, "golang:1.26@sha256:abc", r.PinnedRef())
	})

	t.Run("with tag update", func(t *testing.T) {
		r := Result{Ref: ParseImageRef("redis:7-alpine"), NewDigest: "sha256:abc", NewTagRef: "redis:8-alpine"}
		assert.Equal(t, "redis:8-alpine@sha256:abc", r.PinnedRef())
	})
}

func TestParseAllFiles(t *testing.T) {
	t.Run("dockerfile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM golang:1.26\n"), 0o644))

		result, err := parseAllFiles([]string{path})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0].refs, 1)
		assert.Equal(t, "golang:1.26", result[0].refs[0].TagRef)
		assert.Equal(t, os.FileMode(0o644), result[0].mode.Perm())
	})

	t.Run("compose", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "compose.yml")
		require.NoError(t, os.WriteFile(path, []byte("services:\n  db:\n    image: postgres:18\n"), 0o600))

		result, err := parseAllFiles([]string{path})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0].refs, 1)
		assert.Equal(t, "postgres:18", result[0].refs[0].TagRef)
	})

	t.Run("unrecognized file type", func(t *testing.T) {
		_, err := parseAllFiles([]string{"README.md"})
		assert.ErrorContains(t, err, "unrecognized file type")
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := parseAllFiles([]string{filepath.Join(t.TempDir(), "Dockerfile")})
		assert.Error(t, err)
	})
}

func TestAllRefs(t *testing.T) {
	parsed := []fileData{
		{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27")}},
		{refs: []ImageRef{ParseImageRef("redis:7")}},
	}
	refs := allRefs(parsed)
	require.Len(t, refs, 3)
	assert.Equal(t, "golang:1.26", refs[0].TagRef)
	assert.Equal(t, "nginx:1.27", refs[1].TagRef)
	assert.Equal(t, "redis:7", refs[2].TagRef)
}

func TestAllRefs_empty(t *testing.T) {
	assert.Empty(t, allRefs(nil))
	assert.Empty(t, allRefs([]fileData{{refs: nil}}))
}

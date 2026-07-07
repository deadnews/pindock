package pindock

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
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

func TestImageRef_HasVariable(t *testing.T) {
	assert.False(t, ParseImageRef("golang:1.26").HasVariable())
	assert.True(t, ParseImageRef("golang:${TAG}").HasVariable())
	assert.True(t, ParseImageRef("${BASE_IMAGE}").HasVariable())
	assert.True(t, ParseImageRef("$BASE_IMAGE").HasVariable())
	assert.True(t, ParseImageRef("golang:$TAG").HasVariable())
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
		assert.Equal(t, []string{"golang:1.26", "nginx:1.27"}, refs)
	})

	t.Run("skips pinned without update", func(t *testing.T) {
		data := []fileData{
			{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27@sha256:abc")}},
		}
		refs := collectResolvable(data, false, nil)
		assert.Equal(t, []string{"golang:1.26"}, refs)
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
		assert.Equal(t, StatusUpdated, results[0].Status)
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

	t.Run("current ref with held update reported held", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("xray:26.3.27@sha256:abc")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"xray:26.3.27": "sha256:abc"},
			tagHeld: map[string]string{"xray:26.3.27": "xray:26.6.27"},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusHeld, results[0].Status)
		assert.Equal(t, "xray:26.6.27", results[0].HeldRef)
		assert.Empty(t, repls)
	})

	t.Run("held skipped when digest updates", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("xray:26.3.27@sha256:old")}}
		results, repls := classifyRefs(fp, &resolveData{
			digests: map[string]string{"xray:26.3.27": "sha256:new"},
			tagHeld: map[string]string{"xray:26.3.27": "xray:26.6.27"},
		}, true, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Empty(t, results[0].HeldRef)
		require.Len(t, repls, 1)
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
	assert.Equal(t, []string{"redis:8-alpine"}, refs)
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

func TestTagLookupRefs(t *testing.T) {
	parsed := []fileData{
		{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27@sha256:abc")}},
		{refs: []ImageRef{ParseImageRef("redis:7"), ParseImageRef("scratch"), ParseImageRef("$IMAGE:tag")}},
	}

	t.Run("include pinned", func(t *testing.T) {
		refs := tagLookupRefs(parsed, true)
		assert.Equal(t, []string{"golang:1.26", "nginx:1.27", "redis:7"}, refs)
	})

	t.Run("exclude pinned", func(t *testing.T) {
		refs := tagLookupRefs(parsed, false)
		assert.Equal(t, []string{"golang:1.26", "redis:7"}, refs)
	})
}

func TestTagLookupRefs_empty(t *testing.T) {
	assert.Empty(t, tagLookupRefs(nil, true))
	assert.Empty(t, tagLookupRefs([]fileData{{refs: nil}}, false))
}

func TestRun(t *testing.T) {
	t.Run("scratch only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM scratch\n"), 0o644))

		results, err := Run(context.Background(), []string{path}, false)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "FROM scratch\n", string(data))
	})

	t.Run("variable refs", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM ${BASE_IMAGE}\n"), 0o644))

		results, err := Run(context.Background(), []string{path}, false)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("mixed scratch and pinned without update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		content := "FROM scratch\nFROM golang:1.26@sha256:abc123 AS builder\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		results, err := Run(context.Background(), []string{path}, false)
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, StatusSkipped, results[0].Status)
		assert.Equal(t, StatusCurrent, results[1].Status)
	})

	t.Run("update with scratch only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM scratch\n"), 0o644))

		results, err := Run(context.Background(), []string{path}, true)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("multiple files", func(t *testing.T) {
		dir := t.TempDir()
		df := filepath.Join(dir, "Dockerfile")
		cf := filepath.Join(dir, "compose.yml")
		require.NoError(t, os.WriteFile(df, []byte("FROM scratch\n"), 0o644))
		require.NoError(t, os.WriteFile(cf, []byte("services:\n  app:\n    image: ${IMG}\n"), 0o644))

		results, err := Run(context.Background(), []string{df, cf}, false)
		require.NoError(t, err)
		require.Len(t, results, 2)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := Run(context.Background(), []string{filepath.Join(t.TempDir(), "Dockerfile")}, false)
		assert.Error(t, err)
	})

	t.Run("unrecognized file type", func(t *testing.T) {
		_, err := Run(context.Background(), []string{"README.md"}, false)
		assert.Error(t, err)
	})
}

// startRegistry serves an in-memory registry and returns its host:port.
func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pushTag writes one random image to repo under each tag and returns its digest.
func pushTag(t *testing.T, repo string, tags ...string) string {
	t.Helper()
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	for _, tag := range tags {
		ref, err := name.NewTag(repo + ":" + tag)
		require.NoError(t, err)
		require.NoError(t, remote.Write(ref, img))
	}
	digest, err := img.Digest()
	require.NoError(t, err)
	return digest.String()
}

func TestRun_pinsFilesEndToEnd(t *testing.T) {
	host := startRegistry(t)
	goRepo := host + "/lib/golang"
	staticRepo := host + "/distroless/static"
	pgRepo := host + "/lib/postgres"
	dGo := pushTag(t, goRepo, "1.26-alpine")
	dStatic := pushTag(t, staticRepo, "nonroot")
	dPg := pushTag(t, pgRepo, "18-alpine")

	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	dockerfile := "# build stage\n" +
		"FROM " + goRepo + ":1.26-alpine AS builder\n" +
		"WORKDIR /app\n" +
		"RUN go build \\\n    -o /bin/app ./cmd/app\n\n" +
		"FROM " + staticRepo + ":nonroot\n" +
		"COPY --from=builder /bin/app /bin/app\n"
	require.NoError(t, os.WriteFile(df, []byte(dockerfile), 0o644))

	cf := filepath.Join(dir, "compose.yml")
	compose := "services:\n  db:\n    image: " + pgRepo + ":18-alpine\n  app:\n    image: ${IMG}\n"
	require.NoError(t, os.WriteFile(cf, []byte(compose), 0o600))

	results, err := Run(context.Background(), []string{df, cf}, false)
	require.NoError(t, err)
	require.Len(t, results, 4)
	assert.Equal(t, StatusPinned, results[0].Status)
	assert.Equal(t, StatusPinned, results[1].Status)
	assert.Equal(t, StatusPinned, results[2].Status)
	assert.Equal(t, StatusSkipped, results[3].Status)

	wantDf := "# build stage\n" +
		"FROM " + goRepo + ":1.26-alpine@" + dGo + " AS builder\n" +
		"WORKDIR /app\n" +
		"RUN go build \\\n    -o /bin/app ./cmd/app\n\n" +
		"FROM " + staticRepo + ":nonroot@" + dStatic + "\n" +
		"COPY --from=builder /bin/app /bin/app\n"
	gotDf, err := os.ReadFile(df)
	require.NoError(t, err)
	assert.Equal(t, wantDf, string(gotDf))

	wantCf := "services:\n  db:\n    image: " + pgRepo + ":18-alpine@" + dPg + "\n  app:\n    image: ${IMG}\n"
	gotCf, err := os.ReadFile(cf)
	require.NoError(t, err)
	assert.Equal(t, wantCf, string(gotCf))

	info, err := os.Stat(cf)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Second run is a no-op: everything already pinned.
	results, err = Run(context.Background(), []string{df, cf}, false)
	require.NoError(t, err)
	require.Len(t, results, 4)
	for _, r := range results[:3] {
		assert.Equal(t, StatusCurrent, r.Status)
	}
	gotDf2, err := os.ReadFile(df)
	require.NoError(t, err)
	assert.Equal(t, wantDf, string(gotDf2))
}

func TestCheck(t *testing.T) {
	t.Run("pinned without update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM golang:1.26@sha256:abc123\n"), 0o644))

		results, err := Check(context.Background(), []string{path}, false)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "FROM golang:1.26@sha256:abc123\n", string(data))
	})

	t.Run("scratch and variables", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		content := "FROM scratch\nFROM ${BASE}:latest\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		results, err := Check(context.Background(), []string{path}, false)
		require.NoError(t, err)
		require.Len(t, results, 2)
		for _, r := range results {
			assert.Equal(t, StatusSkipped, r.Status)
		}
	})

	t.Run("update with only variables", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "compose.yml")
		require.NoError(t, os.WriteFile(path, []byte("services:\n  app:\n    image: ${IMG}\n"), 0o644))

		results, err := Check(context.Background(), []string{path}, true)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := Check(context.Background(), []string{filepath.Join(t.TempDir(), "Dockerfile")}, false)
		assert.Error(t, err)
	})
}

func TestRun_resolveLatest(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "8", "8.0.1")

	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	require.NoError(t, os.WriteFile(df, []byte("FROM "+repo+":latest\n"), 0o644))
	cf := filepath.Join(dir, "compose.yml")
	require.NoError(t, os.WriteFile(cf, []byte("services:\n  app:\n    image: "+repo+"\n"), 0o644))

	results, err := Run(context.Background(), []string{df, cf}, false)
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, StatusUpdated, r.Status)
		assert.Equal(t, repo+":8.0.1", r.NewTagRef)
	}

	gotDf, err := os.ReadFile(df)
	require.NoError(t, err)
	assert.Equal(t, "FROM "+repo+":8.0.1@"+digest+"\n", string(gotDf))

	gotCf, err := os.ReadFile(cf)
	require.NoError(t, err)
	assert.Equal(t, "services:\n  app:\n    image: "+repo+":8.0.1@"+digest+"\n", string(gotCf))
}

func TestRun_resolveLatest_migratesPinnedWithUpdate(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "8.0.1")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM "+repo+":latest@sha256:old\n"), 0o644))

	results, err := Run(context.Background(), []string{path}, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusUpdated, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FROM "+repo+":8.0.1@"+digest+"\n", string(got))
}

func TestRun_resolveLatest_pinnedUntouchedWithoutUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Dockerfile")
	content := "FROM redis:latest@sha256:old\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	results, err := Run(context.Background(), []string{path}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusCurrent, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestCheck_heldByLatest(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "1.1", "latest")
	pushTag(t, repo, "2.0")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	content := "FROM " + repo + ":1.1@" + digest + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	results, err := Check(context.Background(), []string{path}, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusHeld, results[0].Status)
	assert.Equal(t, repo+":2.0", results[0].HeldRef)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestRun_resolveLatest_fallsBackToPlainPin(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "bookworm")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM "+repo+":latest\n"), 0o644))

	results, err := Run(context.Background(), []string{path}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusPinned, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FROM "+repo+":latest@"+digest+"\n", string(got))
}

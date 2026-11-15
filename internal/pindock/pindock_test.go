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

	"github.com/deadnews/pindock/internal/registry"
	"github.com/google/go-containerregistry/pkg/name"
	fakeregistry "github.com/google/go-containerregistry/pkg/registry"
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

func TestApplyResults(t *testing.T) {
	at := func(s string, start int) ImageRef {
		ref := ParseImageRef(s)
		ref.Start = start
		return ref
	}

	t.Run("single replacement", func(t *testing.T) {
		content := "FROM golang:1.26-alpine AS builder"
		result := applyResults(content, []Result{
			{Ref: at("golang:1.26-alpine", 5), NewDigest: "sha256:abc", Status: StatusPinned},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:abc AS builder", result)
	})

	t.Run("update digested ref", func(t *testing.T) {
		content := "FROM golang:1.26-alpine@sha256:old AS builder"
		result := applyResults(content, []Result{
			{Ref: at("golang:1.26-alpine@sha256:old", 5), NewDigest: "sha256:new", Status: StatusUpdated},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:new AS builder", result)
	})

	t.Run("multiple replacements same image", func(t *testing.T) {
		content := "FROM golang:1.26 AS a\nFROM golang:1.26 AS b"
		result := applyResults(content, []Result{
			{Ref: at("golang:1.26", 5), NewDigest: "sha256:abc", Status: StatusPinned},
			{Ref: at("golang:1.26", 27), NewDigest: "sha256:abc", Status: StatusPinned},
		})
		assert.Equal(t, "FROM golang:1.26@sha256:abc AS a\nFROM golang:1.26@sha256:abc AS b", result)
	})

	t.Run("mixed digested and unpinned", func(t *testing.T) {
		content := "FROM golang:1.26-alpine@sha256:old AS a\nFROM golang:1.26-alpine AS b"
		result := applyResults(content, []Result{
			{Ref: at("golang:1.26-alpine@sha256:old", 5), NewDigest: "sha256:new", Status: StatusUpdated},
			{Ref: at("golang:1.26-alpine", 45), NewDigest: "sha256:new", Status: StatusPinned},
		})
		assert.Equal(t, "FROM golang:1.26-alpine@sha256:new AS a\nFROM golang:1.26-alpine@sha256:new AS b", result)
	})

	t.Run("distinct refs at different offsets", func(t *testing.T) {
		content := "FROM nginx:1.27 AS web\nFROM golang:1.26-alpine AS builder"
		result := applyResults(content, []Result{
			{Ref: at("nginx:1.27", 5), NewDigest: "sha256:aaa", Status: StatusPinned},
			{Ref: at("golang:1.26-alpine", 28), NewDigest: "sha256:bbb", Status: StatusPinned},
		})
		assert.Equal(t, "FROM nginx:1.27@sha256:aaa AS web\nFROM golang:1.26-alpine@sha256:bbb AS builder", result)
	})

	t.Run("tag move rewrites tag and digest", func(t *testing.T) {
		content := "FROM redis:7-alpine@sha256:old AS cache"
		result := applyResults(content, []Result{
			{
				Ref:       at("redis:7-alpine@sha256:old", 5),
				NewDigest: "sha256:new",
				NewTagRef: "redis:8-alpine",
				Status:    StatusUpdated,
			},
		})
		assert.Equal(t, "FROM redis:8-alpine@sha256:new AS cache", result)
	})

	t.Run("only pinned and updated results change content", func(t *testing.T) {
		content := "FROM scratch\n" +
			"FROM nginx:1.27@sha256:abc\n" +
			"FROM golang:1.26\n" +
			"FROM redis:7@sha256:abc\n" +
			"FROM bad:ref"
		result := applyResults(content, []Result{
			{Ref: at("scratch", 5), Status: StatusSkipped},
			{Ref: at("nginx:1.27@sha256:abc", 18), Status: StatusCurrent},
			{Ref: at("golang:1.26", 45), NewDigest: "sha256:new", Status: StatusPinned},
			{Ref: at("redis:7@sha256:abc", 62), HeldRef: "redis:8", Status: StatusHeld},
			{Ref: at("bad:ref", 86), Status: StatusError, Err: errors.New("not found")},
		})
		assert.Equal(t, "FROM scratch\n"+
			"FROM nginx:1.27@sha256:abc\n"+
			"FROM golang:1.26@sha256:new\n"+
			"FROM redis:7@sha256:abc\n"+
			"FROM bad:ref", result)
	})

	t.Run("does not corrupt comment containing image ref", func(t *testing.T) {
		content := "# Use nginx:1.27 for production\nFROM nginx:1.27"
		result := applyResults(content, []Result{
			{Ref: at("nginx:1.27", 37), NewDigest: "sha256:abc", Status: StatusPinned},
		})
		assert.Equal(t, "# Use nginx:1.27 for production\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("does not corrupt ARG value containing image ref", func(t *testing.T) {
		content := "ARG IMAGE=nginx:1.27\nFROM nginx:1.27"
		result := applyResults(content, []Result{
			{Ref: at("nginx:1.27", 26), NewDigest: "sha256:abc", Status: StatusPinned},
		})
		assert.Equal(t, "ARG IMAGE=nginx:1.27\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("does not corrupt substring in unrelated image", func(t *testing.T) {
		content := "ENV IMG=custom-nginx:1.27\nFROM nginx:1.27"
		result := applyResults(content, []Result{
			{Ref: at("nginx:1.27", 31), NewDigest: "sha256:abc", Status: StatusPinned},
		})
		assert.Equal(t, "ENV IMG=custom-nginx:1.27\nFROM nginx:1.27@sha256:abc", result)
	})

	t.Run("empty", func(t *testing.T) {
		content := "FROM golang:1.26"
		assert.Equal(t, content, applyResults(content, nil))
	})
}

func TestShouldSkip(t *testing.T) {
	assert.False(t, shouldSkip(ParseImageRef("golang:1.26")))
	assert.True(t, shouldSkip(ParseImageRef("scratch")))
	assert.True(t, shouldSkip(ParseImageRef("golang:${TAG}")))
}

func TestClassifyRefs(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("scratch")}}
		results := classifyRefs(fp, nil, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("pinned treated as current without update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:abc")}}
		results := classifyRefs(fp, nil, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
	})

	t.Run("current with update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:abc")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"golang:1.26": {Digest: "sha256:abc"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
	})

	t.Run("unpinned", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"golang:1.26": {Digest: "sha256:abc"},
		}, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		assert.Equal(t, "golang:1.26@sha256:abc", results[0].PinnedRef())
	})

	t.Run("outdated with update", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:old")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"golang:1.26": {Digest: "sha256:new"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Equal(t, "golang:1.26@sha256:new", results[0].PinnedRef())
	})

	t.Run("outdated without update treated as current", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("golang:1.26@sha256:old")}}
		results := classifyRefs(fp, nil, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusCurrent, results[0].Status)
	})

	t.Run("resolve error", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("bad:ref")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"bad:ref": {DigestErr: errors.New("not found")},
		}, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.EqualError(t, results[0].Err, "not found")
	})

	t.Run("missing lookup", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("unknown:ref")}}
		results := classifyRefs(fp, map[string]registry.Lookup{}, false)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.Contains(t, results[0].Err.Error(), "no digest for")
	})
}

func TestClassifyRefs_tagUpdate(t *testing.T) {
	t.Run("tag updated pinned ref", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:old")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"redis:7-alpine": {NewTagRef: "redis:8-alpine", Digest: "sha256:new"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Equal(t, "redis:8-alpine", results[0].NewTagRef)
		assert.Equal(t, "redis:8-alpine@sha256:new", results[0].PinnedRef())
	})

	t.Run("tag updated unpinned ref", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"redis:7-alpine": {NewTagRef: "redis:8-alpine", Digest: "sha256:new"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Equal(t, "redis:8-alpine", results[0].NewTagRef)
		assert.Equal(t, "redis:8-alpine@sha256:new", results[0].PinnedRef())
	})

	t.Run("tag listing error on pinned ref reports error", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine@sha256:old")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"redis:7-alpine": {Digest: "sha256:new", TagErr: errors.New("auth failed")},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusError, results[0].Status)
		assert.Contains(t, results[0].Err.Error(), "auth failed")
	})

	t.Run("current ref with held update reported held", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("xray:26.3.27@sha256:abc")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"xray:26.3.27": {Digest: "sha256:abc", HeldRef: "xray:26.6.27"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusHeld, results[0].Status)
		assert.Equal(t, "xray:26.6.27", results[0].HeldRef)
	})

	t.Run("held skipped when digest updates", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("xray:26.3.27@sha256:old")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"xray:26.3.27": {Digest: "sha256:new", HeldRef: "xray:26.6.27"},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusUpdated, results[0].Status)
		assert.Empty(t, results[0].HeldRef)
	})

	t.Run("tag listing error on unpinned ref still pins", func(t *testing.T) {
		fp := &fileData{path: "Dockerfile", refs: []ImageRef{ParseImageRef("redis:7-alpine")}}
		results := classifyRefs(fp, map[string]registry.Lookup{
			"redis:7-alpine": {Digest: "sha256:abc", TagErr: errors.New("auth failed")},
		}, true)
		require.Len(t, results, 1)
		assert.Equal(t, StatusPinned, results[0].Status)
		assert.Equal(t, "redis:7-alpine@sha256:abc", results[0].PinnedRef())
	})
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

func TestEligibleRefs(t *testing.T) {
	parsed := []fileData{
		{refs: []ImageRef{ParseImageRef("golang:1.26"), ParseImageRef("nginx:1.27@sha256:abc")}},
		{refs: []ImageRef{ParseImageRef("redis:7"), ParseImageRef("scratch"), ParseImageRef("$IMAGE:tag")}},
	}

	t.Run("include pinned", func(t *testing.T) {
		refs := eligibleRefs(parsed, true)
		assert.Equal(t, []string{"golang:1.26", "nginx:1.27", "redis:7"}, refs)
	})

	t.Run("exclude pinned", func(t *testing.T) {
		refs := eligibleRefs(parsed, false)
		assert.Equal(t, []string{"golang:1.26", "redis:7"}, refs)
	})
}

func TestEligibleRefs_empty(t *testing.T) {
	assert.Empty(t, eligibleRefs(nil, true))
	assert.Empty(t, eligibleRefs([]fileData{{refs: nil}}, false))
}

func TestProcess_apply(t *testing.T) {
	t.Run("scratch only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM scratch\n"), 0o644))

		results, err := Process(context.Background(), []string{path}, Options{Apply: true})
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

		results, err := Process(context.Background(), []string{path}, Options{Apply: true})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("mixed scratch and pinned without update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		content := "FROM scratch\nFROM golang:1.26@sha256:abc123 AS builder\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		results, err := Process(context.Background(), []string{path}, Options{Apply: true})
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, StatusSkipped, results[0].Status)
		assert.Equal(t, StatusCurrent, results[1].Status)
	})

	t.Run("update with scratch only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM scratch\n"), 0o644))

		results, err := Process(context.Background(), []string{path}, Options{Apply: true, Update: true})
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

		results, err := Process(context.Background(), []string{df, cf}, Options{Apply: true})
		require.NoError(t, err)
		require.Len(t, results, 2)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := Process(context.Background(), []string{filepath.Join(t.TempDir(), "Dockerfile")}, Options{Apply: true})
		assert.Error(t, err)
	})

	t.Run("unrecognized file type", func(t *testing.T) {
		_, err := Process(context.Background(), []string{"README.md"}, Options{Apply: true})
		assert.Error(t, err)
	})
}

// startRegistry serves an in-memory registry and returns its host:port.
func startRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(fakeregistry.New(fakeregistry.Logger(log.New(io.Discard, "", 0))))
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

func TestProcess_pinsFilesEndToEnd(t *testing.T) {
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
	require.NoError(t, os.Chmod(cf, 0o640))

	results, err := Process(context.Background(), []string{df, cf}, Options{Apply: true})
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
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	// Second run is a no-op: everything already pinned.
	results, err = Process(context.Background(), []string{df, cf}, Options{Apply: true})
	require.NoError(t, err)
	require.Len(t, results, 4)
	for _, r := range results[:3] {
		assert.Equal(t, StatusCurrent, r.Status)
	}
	gotDf2, err := os.ReadFile(df)
	require.NoError(t, err)
	assert.Equal(t, wantDf, string(gotDf2))
}

func TestProcess_reportOnly(t *testing.T) {
	t.Run("pinned without update", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Dockerfile")
		require.NoError(t, os.WriteFile(path, []byte("FROM golang:1.26@sha256:abc123\n"), 0o644))

		results, err := Process(context.Background(), []string{path}, Options{})
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

		results, err := Process(context.Background(), []string{path}, Options{})
		require.NoError(t, err)
		require.Len(t, results, 2)
		for _, r := range results {
			assert.Equal(t, StatusSkipped, r.Status)
		}
	})

	t.Run("update with only variables", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "compose.yml")
		require.NoError(t, os.WriteFile(path, []byte("services:\n  app:\n    image: ${IMG}\n"), 0o644))

		results, err := Process(context.Background(), []string{path}, Options{Update: true})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, StatusSkipped, results[0].Status)
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := Process(context.Background(), []string{filepath.Join(t.TempDir(), "Dockerfile")}, Options{})
		assert.Error(t, err)
	})
}

func TestProcess_resolveLatest(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "8", "8.0.1")

	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	require.NoError(t, os.WriteFile(df, []byte("FROM "+repo+":latest\n"), 0o644))
	cf := filepath.Join(dir, "compose.yml")
	require.NoError(t, os.WriteFile(cf, []byte("services:\n  app:\n    image: "+repo+"\n"), 0o644))

	results, err := Process(context.Background(), []string{df, cf}, Options{Apply: true})
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

func TestProcess_resolveLatest_migratesPinnedWithUpdate(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "8.0.1")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM "+repo+":latest@sha256:old\n"), 0o644))

	results, err := Process(context.Background(), []string{path}, Options{Apply: true, Update: true})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusUpdated, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FROM "+repo+":8.0.1@"+digest+"\n", string(got))
}

func TestProcess_resolveLatest_pinnedUntouchedWithoutUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Dockerfile")
	content := "FROM redis:latest@sha256:old\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	results, err := Process(context.Background(), []string{path}, Options{Apply: true})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusCurrent, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestProcess_heldByLatest(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "1.1", "latest")
	pushTag(t, repo, "2.0")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	content := "FROM " + repo + ":1.1@" + digest + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	results, err := Process(context.Background(), []string{path}, Options{Update: true})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusHeld, results[0].Status)
	assert.Equal(t, repo+":2.0", results[0].HeldRef)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
}

func TestProcess_resolveLatest_fallsBackToPlainPin(t *testing.T) {
	host := startRegistry(t)
	repo := host + "/lib/app"
	digest := pushTag(t, repo, "latest", "bookworm")

	path := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM "+repo+":latest\n"), 0o644))

	results, err := Process(context.Background(), []string{path}, Options{Apply: true})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, StatusPinned, results[0].Status)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "FROM "+repo+":latest@"+digest+"\n", string(got))
}

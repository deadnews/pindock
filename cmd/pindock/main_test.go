package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/deadnews/pindock/internal/pindock"
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

func TestColorLabel(t *testing.T) {
	assert.Equal(t, "PINNED  ", colorLabel("", "PINNED"))
}

func TestDimDigest(t *testing.T) {
	t.Run("with digest", func(t *testing.T) {
		assert.Equal(t, "golang:1.26@sha256:abc", dimDigest("golang:1.26@sha256:abc"))
	})

	t.Run("without digest", func(t *testing.T) {
		assert.Equal(t, "golang:1.26", dimDigest("golang:1.26"))
	})
}

func TestHasVisibleResults(t *testing.T) {
	t.Run("pinned is visible", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusPinned}}
		assert.True(t, hasVisibleResults(results, false))
	})

	t.Run("updated is visible", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusUpdated}}
		assert.True(t, hasVisibleResults(results, false))
	})

	t.Run("error is visible", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusError}}
		assert.True(t, hasVisibleResults(results, false))
	})

	t.Run("current hidden without verbose", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusCurrent}}
		assert.False(t, hasVisibleResults(results, false))
	})

	t.Run("current visible with verbose", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusCurrent}}
		assert.True(t, hasVisibleResults(results, true))
	})

	t.Run("skipped hidden without verbose", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusSkipped}}
		assert.False(t, hasVisibleResults(results, false))
	})

	t.Run("skipped visible with verbose", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusSkipped}}
		assert.True(t, hasVisibleResults(results, true))
	})

	t.Run("no results", func(t *testing.T) {
		assert.False(t, hasVisibleResults(nil, false))
	})
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	fn()
	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintResult(t *testing.T) {
	t.Run("pinned fix mode", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26"),
			NewDigest: "sha256:abc",
			Status:    pindock.StatusPinned,
		}
		out := captureStdout(t, func() { printResult(&r, true, false) })
		assert.Contains(t, out, "PINNED")
		assert.Contains(t, out, "golang:1.26")
		assert.Contains(t, out, "→")
	})

	t.Run("pinned check mode", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26"),
			NewDigest: "sha256:abc",
			Status:    pindock.StatusPinned,
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Contains(t, out, "UNPINNED")
	})

	t.Run("updated fix mode", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26@sha256:old"),
			NewDigest: "sha256:new",
			Status:    pindock.StatusUpdated,
		}
		out := captureStdout(t, func() { printResult(&r, true, false) })
		assert.Contains(t, out, "UPDATED")
		assert.Contains(t, out, "→")
	})

	t.Run("updated check mode", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26@sha256:old"),
			NewDigest: "sha256:new",
			Status:    pindock.StatusUpdated,
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Contains(t, out, "OUTDATED")
	})

	t.Run("current verbose", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("golang:1.26@sha256:abc"),
			Status: pindock.StatusCurrent,
		}
		out := captureStdout(t, func() { printResult(&r, false, true) })
		assert.Contains(t, out, "OK")
	})

	t.Run("current not verbose", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("golang:1.26@sha256:abc"),
			Status: pindock.StatusCurrent,
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Empty(t, out)
	})

	t.Run("skipped verbose", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("scratch"),
			Status: pindock.StatusSkipped,
		}
		out := captureStdout(t, func() { printResult(&r, false, true) })
		assert.Contains(t, out, "SKIP")
	})

	t.Run("skipped not verbose", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("scratch"),
			Status: pindock.StatusSkipped,
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Empty(t, out)
	})

	t.Run("error", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("bad:ref"),
			Status: pindock.StatusError,
			Err:    errors.New("401 unauthorized"),
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Contains(t, out, "ERROR")
		assert.Contains(t, out, "401 unauthorized")
	})
}

func TestPrintResults(t *testing.T) {
	t.Run("groups by file", func(t *testing.T) {
		results := []pindock.Result{
			{File: "Dockerfile", Ref: pindock.ParseImageRef("golang:1.26"), NewDigest: "sha256:abc", Status: pindock.StatusPinned},
			{File: "Dockerfile", Ref: pindock.ParseImageRef("nginx:1.27"), NewDigest: "sha256:def", Status: pindock.StatusPinned},
			{File: "compose.yml", Ref: pindock.ParseImageRef("redis:7"), NewDigest: "sha256:ghi", Status: pindock.StatusPinned},
		}
		out := captureStdout(t, func() { printResults(results, true, false) })
		assert.Contains(t, out, "Dockerfile")
		assert.Contains(t, out, "compose.yml")
	})

	t.Run("skips file group with no visible results", func(t *testing.T) {
		results := []pindock.Result{
			{File: "Dockerfile", Ref: pindock.ParseImageRef("golang:1.26@sha256:abc"), Status: pindock.StatusCurrent},
		}
		out := captureStdout(t, func() { printResults(results, false, false) })
		assert.Empty(t, out)
	})

	t.Run("empty results", func(t *testing.T) {
		out := captureStdout(t, func() { printResults(nil, false, false) })
		assert.Empty(t, out)
	})

	t.Run("multi-line format for pinned", func(t *testing.T) {
		results := []pindock.Result{
			{File: "Dockerfile", Ref: pindock.ParseImageRef("golang:1.26"), NewDigest: "sha256:abc", Status: pindock.StatusPinned},
		}
		out := captureStdout(t, func() { printResults(results, true, false) })
		assert.Contains(t, out, "PINNED")
		assert.Contains(t, out, "golang:1.26\n")
		assert.Contains(t, out, "→ golang:1.26@sha256:abc")
	})
}

func TestSetupColors_noColorEnv(t *testing.T) {
	defer func() {
		colorRed = ""
		colorGreen = ""
		colorYellow = ""
		colorDim = ""
		colorBold = ""
		colorReset = ""
	}()

	colorRed = "\033[31m"
	colorGreen = "\033[32m"
	colorYellow = "\033[33m"
	colorDim = "\033[2m"
	colorBold = "\033[1m"
	colorReset = "\033[0m"

	t.Setenv("NO_COLOR", "1")
	setupColors()

	assert.Empty(t, colorRed)
	assert.Empty(t, colorGreen)
	assert.Empty(t, colorYellow)
	assert.Empty(t, colorDim)
	assert.Empty(t, colorBold)
	assert.Empty(t, colorReset)
}

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/deadnews/pindock/internal/pindock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	t.Run("held visible without verbose", func(t *testing.T) {
		results := []pindock.Result{{Status: pindock.StatusHeld}}
		assert.True(t, hasVisibleResults(results, false))
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

	t.Run("held", func(t *testing.T) {
		r := pindock.Result{
			File:    "Dockerfile",
			Ref:     pindock.ParseImageRef("xray:26.3.27@sha256:abc"),
			HeldRef: "xray:26.6.27",
			Status:  pindock.StatusHeld,
		}
		out := captureStdout(t, func() { printResult(&r, false, false) })
		assert.Contains(t, out, "HELD")
		assert.Contains(t, out, "→ xray:26.6.27  Beyond latest")
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
	defer func() { colorEnabled = false }()

	colorEnabled = true
	t.Setenv("NO_COLOR", "1")
	setupColors()

	assert.False(t, colorEnabled)
}

func TestPaint(t *testing.T) {
	t.Run("disabled returns plain text", func(t *testing.T) {
		assert.Equal(t, "HELD", paint(colorCyan, "HELD"))
	})

	t.Run("enabled wraps in color codes", func(t *testing.T) {
		defer func() { colorEnabled = false }()
		colorEnabled = true
		assert.Equal(t, colorCyan+"HELD"+colorReset, paint(colorCyan, "HELD"))
	})
}

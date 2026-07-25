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

func TestVisible(t *testing.T) {
	tests := []struct {
		name    string
		status  pindock.Status
		verbose bool
		want    bool
	}{
		{"pinned", pindock.StatusPinned, false, true},
		{"updated", pindock.StatusUpdated, false, true},
		{"error", pindock.StatusError, false, true},
		{"held", pindock.StatusHeld, false, true},
		{"current hidden without verbose", pindock.StatusCurrent, false, false},
		{"current shown with verbose", pindock.StatusCurrent, true, true},
		{"skipped hidden without verbose", pindock.StatusSkipped, false, false},
		{"skipped shown with verbose", pindock.StatusSkipped, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := pindock.Result{Status: tt.status}
			assert.Equal(t, tt.want, visible(&r, tt.verbose))
		})
	}
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
	t.Run("pinned applied", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26"),
			NewDigest: "sha256:abc",
			Status:    pindock.StatusPinned,
		}
		out := captureStdout(t, func() { printResult(&r, true) })
		assert.Contains(t, out, "PINNED")
		assert.Contains(t, out, "golang:1.26")
		assert.Contains(t, out, "→")
	})

	t.Run("pinned not applied", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26"),
			NewDigest: "sha256:abc",
			Status:    pindock.StatusPinned,
		}
		out := captureStdout(t, func() { printResult(&r, false) })
		assert.Contains(t, out, "UNPINNED")
	})

	t.Run("updated applied", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26@sha256:old"),
			NewDigest: "sha256:new",
			Status:    pindock.StatusUpdated,
		}
		out := captureStdout(t, func() { printResult(&r, true) })
		assert.Contains(t, out, "UPDATED")
		assert.Contains(t, out, "→")
	})

	t.Run("updated not applied", func(t *testing.T) {
		r := pindock.Result{
			File:      "Dockerfile",
			Ref:       pindock.ParseImageRef("golang:1.26@sha256:old"),
			NewDigest: "sha256:new",
			Status:    pindock.StatusUpdated,
		}
		out := captureStdout(t, func() { printResult(&r, false) })
		assert.Contains(t, out, "OUTDATED")
	})

	t.Run("current", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("golang:1.26@sha256:abc"),
			Status: pindock.StatusCurrent,
		}
		out := captureStdout(t, func() { printResult(&r, false) })
		assert.Contains(t, out, "OK")
	})

	t.Run("skipped", func(t *testing.T) {
		r := pindock.Result{
			File:   "Dockerfile",
			Ref:    pindock.ParseImageRef("scratch"),
			Status: pindock.StatusSkipped,
		}
		out := captureStdout(t, func() { printResult(&r, false) })
		assert.Contains(t, out, "SKIP")
	})

	t.Run("held", func(t *testing.T) {
		r := pindock.Result{
			File:    "Dockerfile",
			Ref:     pindock.ParseImageRef("xray:26.3.27@sha256:abc"),
			HeldRef: "xray:26.6.27",
			Status:  pindock.StatusHeld,
		}
		out := captureStdout(t, func() { printResult(&r, false) })
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
		out := captureStdout(t, func() { printResult(&r, false) })
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
		assert.Equal(t, "Dockerfile\n"+
			"  PINNED    golang:1.26\n          → golang:1.26@sha256:abc\n"+
			"  PINNED    nginx:1.27\n          → nginx:1.27@sha256:def\n"+
			"\ncompose.yml\n"+
			"  PINNED    redis:7\n          → redis:7@sha256:ghi\n\n", out)
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

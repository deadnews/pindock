package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/deadnews/pindock/internal/pindock"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

var colorEnabled bool

func setupColors() {
	_, noColor := os.LookupEnv("NO_COLOR")
	colorEnabled = !noColor && isTerminal()
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in the given color code when color output is enabled.
func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + colorReset
}

// colorLabel renders label padded to a fixed width in the given color.
func colorLabel(code, label string) string {
	return paint(code, fmt.Sprintf("%-8s", label))
}

// dimDigest dims the "@sha256:..." portion, leaving image:tag plain.
func dimDigest(ref string) string {
	if tag, digest, ok := strings.Cut(ref, "@"); ok {
		return tag + paint(colorDim, "@"+digest)
	}
	return ref
}

// printResults prints visible results grouped by file.
func printResults(results []pindock.Result, applied, verbose bool) {
	var file string
	for i := range results {
		r := &results[i]
		if !visible(r, verbose) {
			continue
		}
		if r.File != file {
			if file != "" {
				fmt.Println()
			}
			fmt.Printf("%s\n", paint(colorBold, r.File))
			file = r.File
		}
		printResult(r, applied)
	}
	if file != "" {
		fmt.Println()
	}
}

// visible reports whether a result is shown at the current verbosity.
func visible(r *pindock.Result, verbose bool) bool {
	switch r.Status {
	case pindock.StatusPinned, pindock.StatusUpdated, pindock.StatusError, pindock.StatusHeld:
		return true
	case pindock.StatusCurrent, pindock.StatusSkipped:
		return verbose
	}
	return false
}

func printResult(r *pindock.Result, applied bool) {
	arrow := paint(colorDim, "→")
	switch r.Status {
	case pindock.StatusPinned:
		label := colorLabel(colorRed, "UNPINNED")
		if applied {
			label = colorLabel(colorYellow, "PINNED")
		}
		fmt.Printf("  %s  %s\n          %s %s\n", label, r.Ref.Original, arrow, dimDigest(r.PinnedRef()))
	case pindock.StatusUpdated:
		label := colorLabel(colorRed, "OUTDATED")
		if applied {
			label = colorLabel(colorYellow, "UPDATED")
		}
		fmt.Printf("  %s  %s\n          %s %s\n", label, dimDigest(r.Ref.Original), arrow, dimDigest(r.PinnedRef()))
	case pindock.StatusCurrent:
		fmt.Printf("  %s  %s\n", colorLabel(colorGreen, "OK"), dimDigest(r.Ref.Original))
	case pindock.StatusSkipped:
		fmt.Printf("  %s  %s\n", colorLabel(colorDim, "SKIP"), r.Ref.Original)
	case pindock.StatusHeld:
		fmt.Printf("  %s  %s\n          %s %s  %s\n",
			colorLabel(colorCyan, "HELD"), dimDigest(r.Ref.Original), arrow, r.HeldRef, paint(colorCyan, "Beyond latest"))
	case pindock.StatusError:
		fmt.Printf("  %s  %s  %s\n", colorLabel(colorRed, "ERROR"), r.Ref.TagRef, paint(colorRed, r.Err.Error()))
	}
}

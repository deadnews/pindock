package main

import (
	"fmt"
	"os"
	"slices"
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

// printResults prints results grouped by file.
func printResults(results []pindock.Result, fix, verbose bool) {
	for start := 0; start < len(results); {
		end := start + 1
		for end < len(results) && results[end].File == results[start].File {
			end++
		}
		group := results[start:end]
		start = end

		if !hasVisibleResults(group, verbose) {
			continue
		}
		fmt.Printf("%s\n", paint(colorBold, group[0].File))
		for i := range group {
			printResult(&group[i], fix, verbose)
		}
		fmt.Println()
	}
}

func hasVisibleResults(results []pindock.Result, verbose bool) bool {
	return slices.ContainsFunc(results, func(r pindock.Result) bool {
		switch r.Status {
		case pindock.StatusPinned, pindock.StatusUpdated, pindock.StatusError, pindock.StatusHeld:
			return true
		case pindock.StatusCurrent, pindock.StatusSkipped:
			return verbose
		}
		return false
	})
}

func printResult(r *pindock.Result, fix, verbose bool) {
	arrow := paint(colorDim, "→")
	switch r.Status {
	case pindock.StatusPinned:
		label := colorLabel(colorRed, "UNPINNED")
		if fix {
			label = colorLabel(colorYellow, "PINNED")
		}
		fmt.Printf("  %s  %s\n          %s %s\n", label, r.Ref.Original, arrow, dimDigest(r.PinnedRef()))
	case pindock.StatusUpdated:
		label := colorLabel(colorRed, "OUTDATED")
		if fix {
			label = colorLabel(colorYellow, "UPDATED")
		}
		fmt.Printf("  %s  %s\n          %s %s\n", label, dimDigest(r.Ref.Original), arrow, dimDigest(r.PinnedRef()))
	case pindock.StatusCurrent:
		if verbose {
			fmt.Printf("  %s  %s\n", colorLabel(colorGreen, "OK"), dimDigest(r.Ref.Original))
		}
	case pindock.StatusSkipped:
		if verbose {
			fmt.Printf("  %s  %s\n", colorLabel(colorDim, "SKIP"), r.Ref.Original)
		}
	case pindock.StatusHeld:
		fmt.Printf("  %s  %s\n          %s %s  %s\n",
			colorLabel(colorCyan, "HELD"), dimDigest(r.Ref.Original), arrow, r.HeldRef, paint(colorCyan, "Beyond latest"))
	case pindock.StatusError:
		fmt.Printf("  %s  %s  %s\n", colorLabel(colorRed, "ERROR"), r.Ref.TagRef, paint(colorRed, r.Err.Error()))
	}
}

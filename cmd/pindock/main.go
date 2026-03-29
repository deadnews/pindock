// Package main provides the pindock CLI tool for pinning Docker image digests.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/deadnews/pindock/internal/pindock"
)

var version = "dev"

func main() {
	setupColors()
	ctx := kong.Parse(&CLI{},
		kong.Name("pindock"),
		kong.Description("Pin and update Docker image digests."),
		kong.UsageOnError(),
		kong.Vars{"version": version},
	)
	if err := ctx.Run(); err != nil {
		if errors.Is(err, errCheckFailed) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}

type CLI struct {
	Run     RunCmd           `cmd:"" help:"Pin unpinned image digests."`
	Check   CheckCmd         `cmd:"" help:"Verify all images are pinned."`
	Version kong.VersionFlag `name:"version" short:"V" help:"Print version." hidden:""`
}

// RunCmd pins or updates digests in-place.
type RunCmd struct {
	Files   []string `arg:"" optional:"" help:"Files to process."`
	Dir     string   `short:"C" default:"." help:"Directory to scan."`
	Update  bool     `short:"u" help:"Also update pinned digests to latest."`
	Verbose bool     `short:"v" help:"Show all images, including pinned."`
}

func (cmd *RunCmd) Run() error {
	files, err := resolveFiles(cmd.Files, cmd.Dir)
	if err != nil {
		return err
	}
	if files == nil {
		return nil
	}

	results, err := pindock.Run(context.Background(), files, cmd.Update)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	printResults(results, true, cmd.Verbose)
	return nil
}

// CheckCmd verifies all images are pinned.
type CheckCmd struct {
	Files   []string `arg:"" optional:"" help:"Files to process."`
	Dir     string   `short:"C" default:"." help:"Directory to scan."`
	Verbose bool     `short:"v" help:"Show all images, including pinned."`
}

var errCheckFailed = errors.New("check failed")

func (cmd *CheckCmd) Run() error {
	files, err := resolveFiles(cmd.Files, cmd.Dir)
	if err != nil {
		return err
	}
	if files == nil {
		return nil
	}

	results, err := pindock.Check(context.Background(), files)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	printResults(results, false, cmd.Verbose)

	if slices.ContainsFunc(results, func(r pindock.Result) bool {
		return r.Status == pindock.StatusPinned || r.Status == pindock.StatusError
	}) {
		return errCheckFailed
	}
	return nil
}

// resolveFiles returns nil, nil when no files are found.
func resolveFiles(explicit []string, dir string) ([]string, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}
	files, err := pindock.DiscoverFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no files found")
		return nil, nil
	}
	return files, nil
}

var (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
	colorReset  = "\033[0m"
)

func setupColors() {
	if _, ok := os.LookupEnv("NO_COLOR"); ok || !isTerminal() {
		colorRed = ""
		colorGreen = ""
		colorYellow = ""
		colorDim = ""
		colorBold = ""
		colorReset = ""
	}
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func colorLabel(color, label string) string {
	return color + fmt.Sprintf("%-8s", label) + colorReset
}

// dimDigest dims the "@sha256:..." portion, leaving image:tag plain.
func dimDigest(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i] + colorDim + ref[i:] + colorReset
	}
	return ref
}

func printResults(results []pindock.Result, fix, verbose bool) {
	type group struct {
		file    string
		results []pindock.Result
	}
	seen := make(map[string]int)
	var groups []group
	for _, r := range results {
		if idx, ok := seen[r.File]; ok {
			groups[idx].results = append(groups[idx].results, r)
		} else {
			seen[r.File] = len(groups)
			groups = append(groups, group{file: r.File, results: []pindock.Result{r}})
		}
	}

	first := true
	for _, g := range groups {
		if !hasVisibleResults(g.results, verbose) {
			continue
		}
		if !first {
			fmt.Println()
		}
		first = false
		fmt.Printf("%s%s%s\n", colorBold, g.file, colorReset)
		for i := range g.results {
			printResult(&g.results[i], fix, verbose)
		}
	}
}

func hasVisibleResults(results []pindock.Result, verbose bool) bool {
	for _, r := range results {
		switch r.Status {
		case pindock.StatusPinned, pindock.StatusUpdated, pindock.StatusError:
			return true
		case pindock.StatusCurrent, pindock.StatusSkipped:
			if verbose {
				return true
			}
		}
	}
	return false
}

func printResult(r *pindock.Result, fix, verbose bool) {
	arrow := colorDim + "→" + colorReset
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
	case pindock.StatusError:
		fmt.Fprintf(os.Stderr, "  %s  %s  %s%v%s\n", colorLabel(colorRed, "ERROR"), r.Ref.TagRef, colorRed, r.Err, colorReset)
	}
}

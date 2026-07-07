// Package main provides the pindock CLI tool for pinning Docker image digests.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

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
		kong.ConfigureHelp(kong.HelpOptions{
			Compact:   true,
			FlagsLast: true,
		}),
		kong.Vars{"version": version},
	)
	if err := ctx.Run(); err != nil {
		if errors.Is(err, errCheckFailed) || errors.Is(err, errHasErrors) {
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
	Update  bool     `short:"u" help:"Also update tags and pinned digests to latest."`
	Verbose bool     `short:"v" help:"Show all images, including pinned."`
}

func (cmd *RunCmd) Run() error {
	files, err := resolveFiles(cmd.Files, cmd.Dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no files found")
		return nil
	}

	results, err := pindock.Run(context.Background(), files, cmd.Update)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	printResults(results, true, cmd.Verbose)

	if slices.ContainsFunc(results, func(r pindock.Result) bool {
		return r.Status == pindock.StatusError
	}) {
		return errHasErrors
	}
	return nil
}

// CheckCmd verifies all images are pinned.
type CheckCmd struct {
	Files   []string `arg:"" optional:"" help:"Files to process."`
	Dir     string   `short:"C" default:"." help:"Directory to scan."`
	Update  bool     `short:"u" help:"Also check tags and pinned digests for updates."`
	Verbose bool     `short:"v" help:"Show all images, including pinned."`
}

var (
	errCheckFailed = errors.New("check failed")
	errHasErrors   = errors.New("errors occurred")
)

func (cmd *CheckCmd) Run() error {
	files, err := resolveFiles(cmd.Files, cmd.Dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no files found")
		return nil
	}

	results, err := pindock.Check(context.Background(), files, cmd.Update)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	printResults(results, false, cmd.Verbose)

	if slices.ContainsFunc(results, func(r pindock.Result) bool {
		return r.Status == pindock.StatusPinned || r.Status == pindock.StatusUpdated || r.Status == pindock.StatusError
	}) {
		return errCheckFailed
	}
	return nil
}

func resolveFiles(explicit []string, dir string) ([]string, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}
	files, err := pindock.DiscoverFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}
	return files, nil
}

// Package pindock pins and updates Docker image digests.
package pindock

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/deadnews/pindock/internal/registry"
)

// ImageRef is a parsed Docker image reference.
type ImageRef struct {
	Original string // as found in the file
	TagRef   string // without digest
	Digest   string // empty if unpinned
	Start    int    // byte offset of Original in file content
}

// ParseImageRef splits "image:tag@sha256:..." into tag and digest.
func ParseImageRef(s string) ImageRef {
	if tag, digest, ok := strings.Cut(s, "@"); ok && tag != "" {
		return ImageRef{Original: s, TagRef: tag, Digest: digest}
	}
	return ImageRef{Original: s, TagRef: s}
}

// HasVariable reports whether the ref contains $VAR or ${VAR} substitution.
func (r ImageRef) HasVariable() bool {
	return strings.Contains(r.Original, "$")
}

// Status is a processing outcome for a single image reference.
type Status int

// Processing outcomes.
const (
	StatusPinned  Status = iota // unpinned → pinned
	StatusUpdated               // digest changed
	StatusCurrent               // already up to date
	StatusSkipped               // variable ref, scratch, etc.
	StatusError                 // resolution failed
	StatusHeld                  // newer tag held back beyond latest
)

// Result is the outcome of processing one image reference.
type Result struct {
	File      string
	Ref       ImageRef
	NewDigest string
	NewTagRef string // non-empty if the tag was updated to a newer version
	HeldRef   string // ref of a newer tag beyond latest, when no update was applied
	Status    Status
	Err       error
}

// PinnedRef returns "tag@digest", using NewTagRef if set.
func (r *Result) PinnedRef() string {
	tag := r.Ref.TagRef
	if r.NewTagRef != "" {
		tag = r.NewTagRef
	}
	return tag + "@" + r.NewDigest
}

// Options controls what Process changes.
type Options struct {
	Apply  bool // write resolved refs back to the file
	Update bool // refresh pinned digests and move tags forward
}

type fileData struct {
	path    string
	mode    os.FileMode
	content string
	refs    []ImageRef
}

// Process reports one Result per image ref in files.
// A registry failure is a Result with StatusError; the returned error is a file failure.
func Process(ctx context.Context, files []string, opts Options) ([]Result, error) {
	parsed, err := parseAllFiles(files)
	if err != nil {
		return nil, err
	}

	lookups := registry.Resolve(ctx, eligibleRefs(parsed, opts.Update), opts.Update)

	var results []Result
	for i := range parsed {
		fp := &parsed[i]
		fileResults, repls := classifyRefs(fp, lookups, opts)
		results = append(results, fileResults...)

		if opts.Apply && len(repls) > 0 {
			newContent := applyReplacements(fp.content, repls)
			if err := os.WriteFile(fp.path, []byte(newContent), fp.mode); err != nil {
				return nil, fmt.Errorf("write %s: %w", fp.path, err)
			}
		}
	}

	return results, nil
}

// eligibleRefs collects tag references that need registry lookups.
func eligibleRefs(parsed []fileData, includePinned bool) []string {
	var refs []string
	for _, f := range parsed {
		for _, ref := range f.refs {
			if shouldSkip(ref) || (!includePinned && ref.Digest != "") {
				continue
			}
			refs = append(refs, ref.TagRef)
		}
	}
	return refs
}

// replacement is a position-based text substitution.
type replacement struct {
	start  int
	end    int
	newStr string
}

func classifyRefs(fp *fileData, lookups map[string]registry.Lookup, opts Options) (results []Result, repls []replacement) {
	for _, ref := range fp.refs {
		if shouldSkip(ref) {
			results = append(results, Result{File: fp.path, Ref: ref, Status: StatusSkipped})
			continue
		}

		// Pinned and not updating: no resolution needed.
		if !opts.Update && ref.Digest != "" {
			results = append(results, Result{File: fp.path, Ref: ref, Status: StatusCurrent})
			continue
		}

		lookup := lookups[ref.TagRef]

		// Cannot verify if a newer tag exists: tag listing failed in update mode.
		if ref.Digest != "" && lookup.TagErr != nil {
			results = append(results, Result{
				File:   fp.path,
				Ref:    ref,
				Status: StatusError,
				Err:    lookup.TagErr,
			})
			continue
		}

		if lookup.Digest == "" {
			results = append(results, Result{
				File:   fp.path,
				Ref:    ref,
				Status: StatusError,
				Err:    cmp.Or(lookup.DigestErr, fmt.Errorf("no digest for %s", ref.TagRef)),
			})
			continue
		}

		if lookup.NewTagRef == "" && ref.Digest == lookup.Digest {
			result := Result{File: fp.path, Ref: ref, Status: StatusCurrent}
			if lookup.HeldRef != "" {
				result.Status = StatusHeld
				result.HeldRef = lookup.HeldRef
			}
			results = append(results, result)
			continue
		}

		status := StatusUpdated
		if ref.Digest == "" && lookup.NewTagRef == "" {
			status = StatusPinned
		}
		result := Result{
			File:      fp.path,
			Ref:       ref,
			NewDigest: lookup.Digest,
			NewTagRef: lookup.NewTagRef,
			Status:    status,
		}

		if opts.Apply {
			repls = append(repls, replacement{
				start:  ref.Start,
				end:    ref.Start + len(ref.Original),
				newStr: result.PinnedRef(),
			})
		}

		results = append(results, result)
	}

	return results, repls
}

func parseAllFiles(files []string) ([]fileData, error) {
	result := make([]fileData, 0, len(files))
	for _, f := range files {
		ft, ok := ClassifyFile(f)
		if !ok {
			return nil, fmt.Errorf("unrecognized file type: %s", f)
		}

		info, err := os.Stat(f)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", f, err)
		}
		data, err := os.ReadFile(f) //nolint:gosec // path comes from user args or discovery
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		content := string(data)

		var refs []ImageRef
		switch ft {
		case FileTypeDockerfile:
			refs = ParseDockerfile(content)
		case FileTypeCompose:
			refs = ParseCompose(content)
		}

		result = append(result, fileData{
			path:    f,
			mode:    info.Mode(),
			content: content,
			refs:    refs,
		})
	}
	return result, nil
}

func shouldSkip(ref ImageRef) bool {
	return ref.HasVariable() || ref.TagRef == "scratch"
}

// applyReplacements substitutes refs at their recorded byte offsets.
// Replacements are applied from end to start so earlier offsets stay valid.
func applyReplacements(content string, repls []replacement) string {
	slices.SortFunc(repls, func(a, b replacement) int {
		return cmp.Compare(b.start, a.start)
	})
	for _, r := range repls {
		content = content[:r.start] + r.newStr + content[r.end:]
	}
	return content
}

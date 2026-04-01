// Package pindock pins and updates Docker image digests.
package pindock

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
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
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "@"); i > 0 {
		return ImageRef{Original: s, TagRef: s[:i], Digest: s[i+1:]}
	}
	return ImageRef{Original: s, TagRef: s}
}

// NeedsUpdate reports whether the digest differs from current.
func (r ImageRef) NeedsUpdate(currentDigest string) bool {
	return r.Digest != currentDigest
}

// HasVariable reports whether the ref contains ${...} substitution.
func (r ImageRef) HasVariable() bool {
	return strings.Contains(r.Original, "${")
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
)

// Result is the outcome of processing one image reference.
type Result struct {
	File      string
	Ref       ImageRef
	NewDigest string
	NewTagRef string // non-empty if the tag was updated to a newer version
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

// Run pins digests in-place. If update is true, also refreshes existing ones.
func Run(ctx context.Context, files []string, update bool) ([]Result, error) {
	return process(ctx, files, true, update)
}

// Check reports unpinned images. If update is true, also reports outdated.
func Check(ctx context.Context, files []string, update bool) ([]Result, error) {
	return process(ctx, files, false, update)
}

type fileData struct {
	path    string
	mode    fs.FileMode
	content string
	refs    []ImageRef
}

// resolveData holds digest and tag-update results from registry lookups.
type resolveData struct {
	digests    map[string]string
	errs       map[string]error
	tagUpdates map[string]string
	tagErrors  map[string]error
}

func process(ctx context.Context, files []string, fix, update bool) ([]Result, error) {
	parsed, err := parseAllFiles(files)
	if err != nil {
		return nil, err
	}

	var rd resolveData
	if update {
		rd.tagUpdates, rd.tagErrors = FindLatestTags(ctx, allRefs(parsed))
	}

	toResolve := collectResolvable(parsed, update, rd.tagUpdates)
	rd.digests, rd.errs = ResolveAll(ctx, toResolve)

	var results []Result
	for i := range parsed {
		fp := &parsed[i]
		fileResults, repls := classifyRefs(fp, &rd, fix, update)
		results = append(results, fileResults...)

		if fix && len(repls) > 0 {
			newContent := applyReplacements(fp.content, repls)
			if err := os.WriteFile(fp.path, []byte(newContent), fp.mode); err != nil {
				return nil, fmt.Errorf("write %s: %w", fp.path, err)
			}
		}
	}

	return results, nil
}

func allRefs(parsed []fileData) []ImageRef {
	var refs []ImageRef
	for _, f := range parsed {
		refs = append(refs, f.refs...)
	}
	return refs
}

func collectResolvable(parsed []fileData, update bool, tagUpdates map[string]string) []ImageRef {
	var refs []ImageRef
	for _, f := range parsed {
		for _, ref := range f.refs {
			if shouldSkip(ref) {
				continue
			}
			if !update && ref.Digest != "" {
				continue
			}
			// Use updated tag for resolution if available.
			if newTag, ok := tagUpdates[ref.TagRef]; ok {
				refs = append(refs, ImageRef{TagRef: newTag})
			} else {
				refs = append(refs, ref)
			}
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

func classifyRefs(fp *fileData, rd *resolveData, fix, update bool) (results []Result, repls []replacement) {
	for _, ref := range fp.refs {
		if shouldSkip(ref) {
			results = append(results, Result{File: fp.path, Ref: ref, Status: StatusSkipped})
			continue
		}

		// Pinned and not updating: no resolution needed.
		if !update && ref.Digest != "" {
			results = append(results, Result{File: fp.path, Ref: ref, Status: StatusCurrent})
			continue
		}

		// Cannot verify if a newer tag exists: tag listing failed in update mode.
		if ref.Digest != "" {
			if err, ok := rd.tagErrors[ref.TagRef]; ok {
				results = append(results, Result{File: fp.path, Ref: ref, Status: StatusError, Err: err})
				continue
			}
		}

		// Resolve against updated tag so the digest matches the new version.
		lookupTag := ref.TagRef
		var newTagRef string
		if t, ok := rd.tagUpdates[ref.TagRef]; ok {
			lookupTag = t
			newTagRef = t
		}

		digest, ok := rd.digests[lookupTag]
		if !ok {
			refErr := rd.errs[lookupTag]
			if refErr == nil {
				refErr = fmt.Errorf("failed to resolve %s", lookupTag)
			}
			results = append(results, Result{File: fp.path, Ref: ref, Status: StatusError, Err: refErr})
			continue
		}

		tagChanged := newTagRef != "" && newTagRef != ref.TagRef
		if !tagChanged && !ref.NeedsUpdate(digest) {
			results = append(results, Result{File: fp.path, Ref: ref, NewDigest: digest, Status: StatusCurrent})
			continue
		}

		status := StatusUpdated
		if ref.Digest == "" {
			status = StatusPinned
		}
		result := Result{File: fp.path, Ref: ref, NewDigest: digest, Status: status}
		if tagChanged {
			result.NewTagRef = newTagRef
		}

		if fix {
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

		file, err := os.Open(f) //nolint:gosec // path comes from user args or discovery
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("stat %s: %w", f, err)
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
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

		result = append(result, fileData{path: f, mode: info.Mode(), content: content, refs: refs})
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

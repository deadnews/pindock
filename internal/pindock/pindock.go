// Package pindock pins and updates Docker image digests.
package pindock

import (
	"cmp"
	"context"
	"fmt"
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

// Check reports unpinned images without modifying any files.
func Check(ctx context.Context, files []string) ([]Result, error) {
	return process(ctx, files, false, false)
}

type fileData struct {
	path    string
	mode    fs.FileMode
	content string
	refs    []ImageRef
}

func process(ctx context.Context, files []string, fix, update bool) ([]Result, error) {
	parsed, err := parseAllFiles(files)
	if err != nil {
		return nil, err
	}

	var tagUpdates map[string]string
	if update {
		tagUpdates = FindLatestTags(ctx, allRefs(parsed))
	}

	toResolve := collectResolvable(parsed, update, tagUpdates)
	digests, resolveErrs := ResolveAll(ctx, toResolve)

	var results []Result
	for i := range parsed {
		fp := &parsed[i]
		fileResults, replacements := classifyRefs(fp, digests, resolveErrs, fix, update, tagUpdates)
		results = append(results, fileResults...)

		if fix && len(replacements) > 0 {
			newContent := applyReplacements(fp.content, replacements)
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

func classifyRefs(fp *fileData, digests map[string]string, resolveErrs map[string]error, fix, update bool, tagUpdates map[string]string) (results []Result, replacements map[string]string) {
	replacements = make(map[string]string)

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

		lookupTag := ref.TagRef
		var newTagRef string
		if t, ok := tagUpdates[ref.TagRef]; ok {
			lookupTag = t
			newTagRef = t
		}

		digest, ok := digests[lookupTag]
		if !ok {
			refErr := resolveErrs[lookupTag]
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
			replacements[ref.Original] = result.PinnedRef()
		}

		results = append(results, result)
	}

	return results, replacements
}

func parseAllFiles(files []string) ([]fileData, error) {
	var result []fileData
	for _, f := range files {
		ft, ok := ClassifyFile(f)
		if !ok {
			return nil, fmt.Errorf("unrecognized file type: %s", f)
		}

		data, err := os.ReadFile(f) //nolint:gosec // path comes from user args or discovery
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		content := string(data)

		info, err := os.Stat(f)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", f, err)
		}

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

// applyReplacements replaces old refs. Digested refs go first so that
// unpinned "image:tag" doesn't corrupt "image:tag@sha256:..." elsewhere.
func applyReplacements(content string, replacements map[string]string) string {
	var digested, unpinned []string
	for old := range replacements {
		if strings.Contains(old, "@") {
			digested = append(digested, old)
		} else {
			unpinned = append(unpinned, old)
		}
	}

	slices.SortFunc(digested, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	slices.SortFunc(unpinned, func(a, b string) int { return cmp.Compare(len(b), len(a)) })

	for _, old := range digested {
		content = strings.ReplaceAll(content, old, replacements[old])
	}
	for _, old := range unpinned {
		content = replaceNotFollowedByAt(content, old, replacements[old])
	}
	return content
}

// replaceNotFollowedByAt replaces old with newStr only when not followed by '@'.
func replaceNotFollowedByAt(content, old, newStr string) string {
	var b strings.Builder
	b.Grow(len(content))
	for {
		i := strings.Index(content, old)
		if i == -1 {
			b.WriteString(content)
			break
		}
		end := i + len(old)
		if end < len(content) && content[end] == '@' {
			// Part of a digested ref: skip.
			b.WriteString(content[:end])
			content = content[end:]
			continue
		}
		b.WriteString(content[:i])
		b.WriteString(newStr)
		content = content[end:]
	}
	return b.String()
}

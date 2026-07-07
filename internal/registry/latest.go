package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// maxLatestCandidates bounds digest checks when latest matches an old or no tag.
const maxLatestCandidates = 20

// ResolveLatestTags finds the version tag sharing each latest or untagged
// reference's digest. Returns old→new updates and per-ref listing errors.
func ResolveLatestTags(ctx context.Context, tagRefs []string) (updates map[string]string, failed map[string]error) {
	refs, repos := taggedRefs(tagRefs, true)

	aliases, errs := runConcurrently(repos, func(repo name.Repository) (string, error) {
		return resolveLatestAlias(ctx, repo)
	})

	updates = make(map[string]string)
	failed = make(map[string]error)
	for _, ref := range refs {
		if err, ok := errs[ref.repoStr]; ok {
			failed[ref.tagRef] = fmt.Errorf("list tags for %s: %w", ref.repoStr, err)
			continue
		}
		if alias := aliases[ref.repoStr]; alias != "" {
			updates[ref.tagRef] = replaceTag(ref.tagRef, alias)
		}
	}

	return updates, failed
}

// resolveLatestAlias returns the newest version tag sharing latest's digest, or "".
func resolveLatestAlias(ctx context.Context, repo name.Repository) (string, error) {
	tags, err := listTags(ctx, repo)
	if err != nil {
		return "", err
	}
	return latestAliasFromTags(ctx, repo, tags), nil
}

// latestAliasFromTags walks candidate tags for one whose digest matches latest.
func latestAliasFromTags(ctx context.Context, repo name.Repository, tags []string) string {
	latestDigest, err := headDigest(ctx, repo.Tag("latest"))
	if err != nil {
		return ""
	}

	candidates := latestCandidates(tags)
	if len(candidates) > maxLatestCandidates {
		candidates = candidates[:maxLatestCandidates]
	}
	for _, tag := range candidates {
		digest, err := headDigest(ctx, repo.Tag(tag))
		if err == nil && digest == latestDigest {
			return tag
		}
	}
	return ""
}

// latestCandidates returns suffix-free versioned tags, newest first, with
// bare tags before prefixed ones: prefixed numbers may be dates, not versions.
func latestCandidates(tags []string) []string {
	type candidate struct {
		raw     string
		version []int
		bare    bool
	}
	var cands []candidate
	for _, tag := range tags {
		parsed, ok := parseVersionedTag(tag)
		if !ok || parsed.suffix != "" {
			continue
		}
		cands = append(cands, candidate{raw: tag, version: parsed.version, bare: parsed.prefix == ""})
	}
	slices.SortFunc(cands, func(a, b candidate) int {
		if a.bare != b.bare {
			if a.bare {
				return -1
			}
			return 1
		}
		if c := slices.Compare(b.version, a.version); c != 0 {
			return c
		}
		return strings.Compare(a.raw, b.raw)
	})

	result := make([]string, len(cands))
	for i, c := range cands {
		result[i] = c.raw
	}
	return result
}

// replaceTag swaps or appends the tag portion of a reference.
func replaceTag(tagRef, tag string) string {
	if i := strings.LastIndex(tagRef, ":"); i > strings.LastIndex(tagRef, "/") {
		return tagRef[:i+1] + tag
	}
	return tagRef + ":" + tag
}

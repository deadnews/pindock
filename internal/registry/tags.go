package registry

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

var (
	versionRe    = regexp.MustCompile(`^(.*?)(\d+(?:\.\d+)*)(.*)$`)
	prereleaseRe = regexp.MustCompile(`^([-_.]?(?:alpha|beta|rc)[-_.]?)(\d+)$`)
)

type parsedTag struct {
	prefix     string
	version    []int
	suffix     string
	prerelease bool
}

// parseVersionedTag extracts prefix, version numbers, and suffix from tags
// like "7-alpine", "alpine-1.23.5", "v1.26.0", or "1.2.3-rc.1".
func parseVersionedTag(tag string) (parsedTag, bool) {
	m := versionRe.FindStringSubmatch(tag)
	if m == nil {
		return parsedTag{}, false
	}
	parts := strings.Split(m[2], ".")
	suffix := m[3]
	prerelease := false
	if pr := prereleaseRe.FindStringSubmatch(suffix); pr != nil {
		suffix = pr[1]
		parts = append(parts, pr[2])
		prerelease = true
	}
	version := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return parsedTag{}, false
		}
		version[i] = n
	}
	return parsedTag{prefix: m[1], version: version, suffix: suffix, prerelease: prerelease}, true
}

// findLatestTag finds the latest tag with the same prefix, suffix, and depth,
// staying at or below limit. A prerelease graduates to the newest stable tag
// at or above its base version, else follows its own stream uncapped.
func findLatestTag(currentTag string, allTags []string, limit []int) (string, bool) {
	current, ok := parseVersionedTag(currentTag)
	if !ok {
		return "", false
	}
	if current.prerelease {
		base := current.version[:len(current.version)-1]
		stable := parsedTag{prefix: current.prefix, version: base}
		if tag, version := newestMatch(allTags, stable, limit); tag != "" && slices.Compare(version, base) >= 0 {
			return tag, true
		}
		limit = nil
	}
	tag, version := newestMatch(allTags, current, limit)
	if tag == "" || slices.Compare(version, current.version) <= 0 {
		return "", false
	}
	return tag, true
}

// newestMatch returns the newest tag matching want's
// prefix, suffix, and version depth, staying at or below limit.
func newestMatch(allTags []string, want parsedTag, limit []int) (tag string, version []int) {
	for _, t := range allTags {
		parsed, ok := parseVersionedTag(t)
		if !ok || parsed.prefix != want.prefix || parsed.suffix != want.suffix || len(parsed.version) != len(want.version) {
			continue
		}
		if exceedsLimit(parsed.version, limit) {
			continue
		}
		if tag == "" || slices.Compare(parsed.version, version) > 0 {
			tag = t
			version = parsed.version
		}
	}
	return tag, version
}

// exceedsLimit reports whether version is newer than limit at limit's depth.
func exceedsLimit(version, limit []int) bool {
	if limit == nil {
		return false
	}
	d := min(len(version), len(limit))
	return slices.Compare(version[:d], limit[:d]) > 0
}

// taggedRef is a tag reference resolved to its repository and bare tag.
type taggedRef struct {
	tagRef  string
	tag     string
	repoStr string
}

// taggedRefs parses unique tag references, keeping versioned ones only if versioned.
func taggedRefs(tagRefs []string, versioned bool) (refs []taggedRef, repos map[string]name.Repository) {
	repos = make(map[string]name.Repository)
	seen := make(map[string]bool)

	for _, tagRef := range tagRefs {
		if seen[tagRef] {
			continue
		}
		seen[tagRef] = true
		parsed, err := name.ParseReference(tagRef)
		if err != nil {
			continue
		}
		tagged, ok := parsed.(name.Tag)
		if !ok {
			continue
		}
		tag := tagged.TagStr()
		if tag != "latest" && !versioned {
			continue
		}
		repoStr := tagged.Context().String()
		refs = append(refs, taggedRef{tagRef: tagRef, tag: tag, repoStr: repoStr})
		repos[repoStr] = tagged.Context()
	}
	return refs, repos
}

// FindTagUpdates resolves latest and untagged references to the version tag
// sharing their digest. When versioned is true, version tags also move to the
// newest release, capped at the version latest points to; blocked moves are held.
func FindTagUpdates(ctx context.Context, tagRefs []string, versioned bool) (updates, held map[string]string, failed map[string]error) {
	refs, repos := taggedRefs(tagRefs, versioned)

	lookups, repoErrors := runConcurrently(repos, func(repo name.Repository) (repoLookup, error) {
		return lookupRepo(ctx, repo)
	})

	updates = make(map[string]string)
	held = make(map[string]string)
	failed = make(map[string]error)
	for _, ref := range refs {
		if err, ok := repoErrors[ref.repoStr]; ok {
			failed[ref.tagRef] = fmt.Errorf("list tags for %s: %w", ref.repoStr, err)
			continue
		}
		lookup := lookups[ref.repoStr]

		if ref.tag == "latest" {
			if lookup.alias != "" {
				updates[ref.tagRef] = replaceTag(ref.tagRef, lookup.alias)
			}
			continue
		}
		if newTag, ok := findLatestTag(ref.tag, lookup.tags, lookup.limit); ok {
			updates[ref.tagRef] = replaceTag(ref.tagRef, newTag)
			continue
		}
		if lookup.limit == nil {
			continue
		}
		if heldTag, ok := findLatestTag(ref.tag, lookup.tags, nil); ok {
			held[ref.tagRef] = replaceTag(ref.tagRef, heldTag)
		}
	}

	return updates, held, failed
}

// repoLookup holds a repository's tags and the version tag latest points at.
type repoLookup struct {
	tags  []string
	alias string // version tag sharing latest's digest; empty if none
	limit []int  // alias version; nil leaves updates uncapped
}

// lookupRepo lists a repository's tags and resolves its latest alias.
func lookupRepo(ctx context.Context, repo name.Repository) (repoLookup, error) {
	tags, err := listTags(ctx, repo)
	if err != nil {
		return repoLookup{}, err
	}

	lookup := repoLookup{tags: tags, alias: latestAliasFromTags(ctx, repo, tags)}
	if parsed, ok := parseVersionedTag(lookup.alias); ok {
		lookup.limit = parsed.version
	}
	return lookup, nil
}

package pindock

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var versionRe = regexp.MustCompile(`^(.*?)(\d+(?:\.\d+)*)(.*)$`)

type parsedTag struct {
	Prefix  string
	Version []int
	Suffix  string
}

// parseVersionedTag extracts prefix, version numbers, and suffix from tags
// like "7-alpine", "alpine-1.23.5", or "v1.26.0".
func parseVersionedTag(tag string) (parsedTag, bool) {
	m := versionRe.FindStringSubmatch(tag)
	if m == nil {
		return parsedTag{}, false
	}
	parts := strings.Split(m[2], ".")
	version := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return parsedTag{}, false
		}
		version[i] = n
	}
	return parsedTag{Prefix: m[1], Version: version, Suffix: m[3]}, true
}

// compareVersions returns -1, 0, or 1.
func compareVersions(a, b []int) int {
	for i := range max(len(a), len(b)) {
		va, vb := 0, 0
		if i < len(a) {
			va = a[i]
		}
		if i < len(b) {
			vb = b[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// findLatestTag finds the latest tag matching the same prefix, suffix, and version depth.
func findLatestTag(currentTag string, allTags []string) (string, bool) {
	current, ok := parseVersionedTag(currentTag)
	if !ok {
		return "", false
	}
	depth := len(current.Version)
	var best parsedTag
	var bestRaw string
	for _, t := range allTags {
		parsed, ok := parseVersionedTag(t)
		if !ok || parsed.Prefix != current.Prefix || parsed.Suffix != current.Suffix || len(parsed.Version) != depth {
			continue
		}
		if bestRaw == "" || compareVersions(parsed.Version, best.Version) > 0 {
			best = parsed
			bestRaw = t
		}
	}
	if bestRaw == "" || compareVersions(best.Version, current.Version) <= 0 {
		return "", false
	}
	return bestRaw, true
}

// FindLatestTags queries registries for newer versions of each ref.
// Returns old→new tag updates and per-ref errors for failed repo listings.
func FindLatestTags(ctx context.Context, refs []ImageRef) (updates map[string]string, failed map[string]error) {
	updates = make(map[string]string)
	failed = make(map[string]error)

	type refInfo struct {
		tagRef string
		tag    string
		repo   name.Repository
	}
	var infos []refInfo
	repos := make(map[string]name.Repository)

	for _, ref := range refs {
		if shouldSkip(ref) {
			continue
		}
		parsed, err := name.ParseReference(ref.TagRef)
		if err != nil {
			continue
		}
		tagged, ok := parsed.(name.Tag)
		if !ok {
			continue
		}
		tag := tagged.TagStr()
		if tag == "latest" {
			continue
		}
		repo := tagged.Context()
		repoStr := repo.String()
		infos = append(infos, refInfo{tagRef: ref.TagRef, tag: tag, repo: repo})
		repos[repoStr] = repo
	}

	tagCache := make(map[string][]string)
	repoErrors := make(map[string]error)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)

	for repoStr, repo := range repos {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			tags, err := remote.List(repo,
				remote.WithAuthFromKeychain(authn.DefaultKeychain),
				remote.WithContext(ctx),
			)

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				tagCache[repoStr] = tags
			} else {
				repoErrors[repoStr] = simplifyError(err)
			}
		})
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, info := range infos {
		if seen[info.tagRef] {
			continue
		}
		seen[info.tagRef] = true

		repoStr := info.repo.String()
		if err, ok := repoErrors[repoStr]; ok {
			failed[info.tagRef] = fmt.Errorf("list tags for %s: %w", repoStr, err)
			continue
		}

		newTag, found := findLatestTag(info.tag, tagCache[repoStr])
		if !found {
			continue
		}

		colonIdx := strings.LastIndex(info.tagRef, ":")
		if colonIdx < 0 {
			continue
		}
		updates[info.tagRef] = info.tagRef[:colonIdx+1] + newTag
	}

	return updates, failed
}

package registry

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var (
	versionRe    = regexp.MustCompile(`^(.*?)(\d+(?:\.\d+)*)(.*)$`)
	prereleaseRe = regexp.MustCompile(`^([-_.]?(?:alpha|beta|rc)[-_.]?)(\d+)$`)
)

type parsedTag struct {
	prefix  string
	version []int
	suffix  string
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
	if pr := prereleaseRe.FindStringSubmatch(suffix); pr != nil {
		suffix = pr[1]
		parts = append(parts, pr[2])
	}
	version := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return parsedTag{}, false
		}
		version[i] = n
	}
	return parsedTag{prefix: m[1], version: version, suffix: suffix}, true
}

// findLatestTag finds the latest tag matching the same prefix, suffix, and version depth.
func findLatestTag(currentTag string, allTags []string) (string, bool) {
	current, ok := parseVersionedTag(currentTag)
	if !ok {
		return "", false
	}
	depth := len(current.version)
	var best parsedTag
	var bestRaw string
	for _, t := range allTags {
		parsed, ok := parseVersionedTag(t)
		if !ok || parsed.prefix != current.prefix || parsed.suffix != current.suffix || len(parsed.version) != depth {
			continue
		}
		if bestRaw == "" || slices.Compare(parsed.version, best.version) > 0 {
			best = parsed
			bestRaw = t
		}
	}
	if bestRaw == "" || slices.Compare(best.version, current.version) <= 0 {
		return "", false
	}
	return bestRaw, true
}

// FindLatestTags queries registries for newer versions of each tag reference.
// Returns old→new tag updates and per-ref errors for failed repo listings.
func FindLatestTags(ctx context.Context, tagRefs []string) (updates map[string]string, failed map[string]error) {
	updates = make(map[string]string)
	failed = make(map[string]error)

	type refInfo struct {
		tagRef  string
		tag     string
		repoStr string
	}
	var infos []refInfo
	repos := make(map[string]name.Repository)
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
		if tag == "latest" {
			continue
		}
		repo := tagged.Context()
		repoStr := repo.String()
		infos = append(infos, refInfo{tagRef: tagRef, tag: tag, repoStr: repoStr})
		repos[repoStr] = repo
	}

	tagCache, repoErrors := listRepoTags(ctx, repos)

	for _, info := range infos {
		if err, ok := repoErrors[info.repoStr]; ok {
			failed[info.tagRef] = fmt.Errorf("list tags for %s: %w", info.repoStr, err)
			continue
		}

		newTag, found := findLatestTag(info.tag, tagCache[info.repoStr])
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

// listRepoTags lists all tags for each repository concurrently.
func listRepoTags(ctx context.Context, repos map[string]name.Repository) (tags map[string][]string, errs map[string]error) {
	tags = make(map[string][]string, len(repos))
	errs = make(map[string]error)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)

	for repoStr, repo := range repos {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			list, err := remote.List(repo,
				remote.WithAuthFromKeychain(authn.DefaultKeychain),
				remote.WithContext(ctx),
			)

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				tags[repoStr] = list
			} else {
				errs[repoStr] = simplifyError(err)
			}
		})
	}
	wg.Wait()
	return tags, errs
}

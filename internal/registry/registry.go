// Package registry resolves image digests and latest tags from registries.
package registry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const maxConcurrency = 10

// puller authenticates once per repository and reuses that across calls.
var puller = func() *remote.Puller {
	p, err := remote.NewPuller(remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		panic(err)
	}
	return p
}()

// Lookup is the registry's answer for one tag reference.
type Lookup struct {
	NewTagRef string // tag ref it moves to; empty if it stays
	Digest    string // of NewTagRef if set, else of the reference; empty on DigestErr
	HeldRef   string // newer tag ref beyond latest, not applied
	TagErr    error  // tag listing failed: NewTagRef and HeldRef are unknown
	DigestErr error
}

// Resolve looks up each tag reference, keyed by the reference as given.
// Without update, only latest and untagged references move to another tag.
func Resolve(ctx context.Context, tagRefs []string, update bool) map[string]Lookup {
	updates, held, tagErrs := findTagUpdates(ctx, tagRefs, update)

	// Resolve against updated tags so digests match the new versions.
	targets := make([]string, len(tagRefs))
	for i, ref := range tagRefs {
		targets[i] = cmp.Or(updates[ref], ref)
	}
	digests, errs := resolveAll(ctx, targets)

	lookups := make(map[string]Lookup, len(tagRefs))
	for i, ref := range tagRefs {
		lookups[ref] = Lookup{
			NewTagRef: updates[ref],
			Digest:    digests[targets[i]],
			HeldRef:   held[ref],
			TagErr:    tagErrs[ref],
			DigestErr: errs[targets[i]],
		}
	}
	return lookups
}

// resolveDigest fetches the current digest for a tag reference.
func resolveDigest(ctx context.Context, tagRef string) (string, error) {
	ref, err := name.ParseReference(tagRef)
	if err != nil {
		return "", fmt.Errorf("parse reference %q: %w", tagRef, err)
	}
	return headDigest(ctx, ref)
}

// headDigest fetches the manifest digest for a parsed reference.
func headDigest(ctx context.Context, ref name.Reference) (string, error) {
	desc, err := puller.Head(ctx, ref)
	if err != nil {
		return "", simplifyError(err)
	}
	return desc.Digest.String(), nil
}

// simplifyError extracts the HTTP status from registry transport errors.
func simplifyError(err error) error {
	if terr, ok := errors.AsType[*transport.Error](err); ok {
		text := strings.ToLower(http.StatusText(terr.StatusCode))
		status := strings.ReplaceAll(text, " ", "-")
		return fmt.Errorf("%d %s", terr.StatusCode, status)
	}
	return err
}

// listTags lists all tags in a repository.
func listTags(ctx context.Context, repo name.Repository) ([]string, error) {
	tags, err := puller.List(ctx, repo)
	if err != nil {
		return nil, simplifyError(err)
	}
	return tags, nil
}

// resolveAll resolves digests for unique tag references concurrently.
func resolveAll(ctx context.Context, tagRefs []string) (digests map[string]string, errs map[string]error) {
	unique := make(map[string]string, len(tagRefs))
	for _, t := range tagRefs {
		unique[t] = t
	}
	return runConcurrently(unique, func(tagRef string) (string, error) {
		return resolveDigest(ctx, tagRef)
	})
}

// runConcurrently applies fn to each item with bounded concurrency,
// collecting per-key results and errors.
func runConcurrently[K comparable, T, V any](items map[K]T, fn func(T) (V, error)) (results map[K]V, errs map[K]error) {
	results = make(map[K]V, len(items))
	errs = make(map[K]error)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)

	for key, item := range items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := fn(item)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[key] = err
			} else {
				results[key] = result
			}
		})
	}
	wg.Wait()
	return results, errs
}

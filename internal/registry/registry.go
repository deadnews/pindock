// Package registry resolves image digests and latest tags from registries.
package registry

import (
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

// ResolveDigest fetches the current digest for a tag reference.
func ResolveDigest(ctx context.Context, tagRef string) (string, error) {
	ref, err := name.ParseReference(tagRef)
	if err != nil {
		return "", fmt.Errorf("parse reference %q: %w", tagRef, err)
	}
	return headDigest(ctx, ref)
}

// headDigest fetches the manifest digest for a parsed reference.
func headDigest(ctx context.Context, ref name.Reference) (string, error) {
	desc, err := remote.Head(ref,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return "", simplifyError(err)
	}
	return desc.Digest.String(), nil
}

// simplifyError extracts the HTTP status from registry transport errors.
func simplifyError(err error) error {
	if terr, ok := errors.AsType[*transport.Error](err); ok {
		status := strings.ReplaceAll(strings.ToLower(http.StatusText(terr.StatusCode)), " ", "-")
		return fmt.Errorf("%d %s", terr.StatusCode, status)
	}
	return err
}

// listTags lists all tags in a repository.
func listTags(ctx context.Context, repo name.Repository) ([]string, error) {
	tags, err := remote.List(repo,
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithContext(ctx),
	)
	if err != nil {
		return nil, simplifyError(err)
	}
	return tags, nil
}

// ResolveAll resolves digests for unique tag references concurrently.
func ResolveAll(ctx context.Context, tagRefs []string) (digests map[string]string, errs map[string]error) {
	unique := make(map[string]string, len(tagRefs))
	for _, t := range tagRefs {
		unique[t] = t
	}
	return runConcurrently(unique, func(tagRef string) (string, error) {
		return ResolveDigest(ctx, tagRef)
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

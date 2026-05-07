package pindock

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

// ResolveAll resolves digests for unique tag references concurrently.
func ResolveAll(ctx context.Context, tagRefs []string) (digests map[string]string, errs map[string]error) {
	unique := make(map[string]struct{})
	for _, t := range tagRefs {
		unique[t] = struct{}{}
	}

	digests = make(map[string]string, len(unique))
	errs = make(map[string]error)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)

	for tagRef := range unique {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			digest, err := ResolveDigest(ctx, tagRef)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[tagRef] = err
			} else {
				digests[tagRef] = digest
			}
		})
	}

	wg.Wait()
	return digests, errs
}

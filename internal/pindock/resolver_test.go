package pindock

import (
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
)

func TestSimplifyError(t *testing.T) {
	t.Run("transport error", func(t *testing.T) {
		err := &transport.Error{StatusCode: 404}
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "404 not-found")
	})

	t.Run("transport error unauthorized", func(t *testing.T) {
		err := &transport.Error{StatusCode: 401}
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "401 unauthorized")
	})

	t.Run("non-transport error passed through", func(t *testing.T) {
		err := errors.New("connection refused")
		simplified := simplifyError(err)
		assert.EqualError(t, simplified, "connection refused")
	})
}

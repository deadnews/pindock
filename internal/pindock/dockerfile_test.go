package pindock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDockerfile(t *testing.T) {
	t.Run("simple FROM", func(t *testing.T) {
		refs := ParseDockerfile(`FROM golang:1.26-alpine`)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
		assert.Empty(t, refs[0].Digest)
	})

	t.Run("FROM with digest", func(t *testing.T) {
		refs := ParseDockerfile(`FROM golang:1.26-alpine@sha256:abc123 AS builder`)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
		assert.Equal(t, "sha256:abc123", refs[0].Digest)
	})

	t.Run("FROM with platform", func(t *testing.T) {
		refs := ParseDockerfile(`FROM --platform=linux/amd64 golang:1.26-alpine@sha256:abc AS builder`)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
	})

	t.Run("FROM with variable platform", func(t *testing.T) {
		refs := ParseDockerfile(`FROM --platform=${BUILDPLATFORM} golang:1.26-alpine AS builder`)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
	})

	t.Run("FROM scratch", func(t *testing.T) {
		refs := ParseDockerfile(`FROM scratch`)
		require.Len(t, refs, 1)
		assert.Equal(t, "scratch", refs[0].TagRef)
	})

	t.Run("FROM with variable image braces", func(t *testing.T) {
		refs := ParseDockerfile(`FROM ${BASE_IMAGE}`)
		require.Len(t, refs, 1)
		assert.True(t, refs[0].HasVariable())
	})

	t.Run("FROM with variable image bare", func(t *testing.T) {
		refs := ParseDockerfile(`FROM $BASE_IMAGE`)
		require.Len(t, refs, 1)
		assert.True(t, refs[0].HasVariable())
	})

	t.Run("COPY --from with bare variable skipped", func(t *testing.T) {
		content := "FROM golang:1.26 AS builder\nCOPY --from=$TOOL_IMAGE /bin/tool /bin/tool"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.False(t, refs[0].HasVariable())
		assert.True(t, refs[1].HasVariable())
	})

	t.Run("COPY --from image", func(t *testing.T) {
		content := `FROM golang:1.26 AS builder
COPY --from=ghcr.io/org/tool:1.0@sha256:def /bin/tool /bin/tool`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
		assert.Equal(t, "ghcr.io/org/tool:1.0", refs[1].TagRef)
		assert.Equal(t, "sha256:def", refs[1].Digest)
	})

	t.Run("COPY --from stage name skipped", func(t *testing.T) {
		content := `FROM golang:1.26 AS builder
COPY --from=builder /app /app`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})

	t.Run("COPY --from numeric stage skipped", func(t *testing.T) {
		content := `FROM golang:1.26
COPY --from=0 /app /app`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
	})

	t.Run("RUN --mount=from image", func(t *testing.T) {
		content := `FROM golang:1.26
RUN --mount=type=bind,from=ghcr.io/astral-sh/uv:0.5,target=/bin/uv uv sync`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
		assert.Equal(t, "ghcr.io/astral-sh/uv:0.5", refs[1].TagRef)
	})

	t.Run("RUN --mount=from stage name skipped", func(t *testing.T) {
		content := `FROM golang:1.26 AS builder
RUN --mount=from=builder,target=/src ls /src`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
	})

	t.Run("RUN --mount cache without from skipped", func(t *testing.T) {
		content := `FROM golang:1.26
RUN --mount=type=cache,target=/go/pkg go build`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
	})

	t.Run("multi-stage full Dockerfile", func(t *testing.T) {
		content := `FROM golang:1.26-alpine@sha256:aaa AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg go mod download
COPY . .
RUN go build -o /bin/app

FROM gcr.io/distroless/static@sha256:bbb AS runtime
COPY --from=ghcr.io/org/check:1.0@sha256:ccc /bin/check /bin/check
COPY --from=builder /bin/app /bin/app
ENTRYPOINT ["/bin/app"]`

		refs := ParseDockerfile(content)
		require.Len(t, refs, 3)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
		assert.Equal(t, "gcr.io/distroless/static", refs[1].TagRef)
		assert.Equal(t, "ghcr.io/org/check:1.0", refs[2].TagRef)
	})

	t.Run("line continuation", func(t *testing.T) {
		content := "FROM --platform=linux/amd64 \\\n    golang:1.26-alpine AS builder"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26-alpine", refs[0].TagRef)
	})

	t.Run("RUN mount with continuation", func(t *testing.T) {
		content := "FROM golang:1.26\nRUN --mount=type=cache,target=/cache \\\n    --mount=from=ghcr.io/tool:1.0,target=/tool \\\n    /tool/run"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
		assert.Equal(t, "ghcr.io/tool:1.0", refs[1].TagRef)
	})

	t.Run("comments and empty lines ignored", func(t *testing.T) {
		content := "# This is a comment\n\nFROM golang:1.26\n# Another comment"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
	})

	t.Run("trailing continuation line", func(t *testing.T) {
		content := "FROM golang:1.26 \\"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})

	t.Run("FROM with only flags no image", func(t *testing.T) {
		refs := ParseDockerfile("FROM --platform=linux/amd64")
		assert.Empty(t, refs)
	})

	t.Run("FROM AS without image", func(t *testing.T) {
		refs := ParseDockerfile("FROM AS builder")
		assert.Empty(t, refs)
	})

	t.Run("RUN without mount flags", func(t *testing.T) {
		content := "FROM golang:1.26\nRUN echo hello"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})

	t.Run("RUN with non-mount flag before mount", func(t *testing.T) {
		content := "FROM golang:1.26\nRUN --security=insecure --mount=from=ghcr.io/tool:1.0,target=/t cmd"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.Equal(t, "ghcr.io/tool:1.0", refs[1].TagRef)
	})

	t.Run("FROM stage alias skipped", func(t *testing.T) {
		content := `FROM python:3.14-alpine@sha256:abc AS base
FROM base AS builder`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "python:3.14-alpine", refs[0].TagRef)
	})

	t.Run("FROM stage alias with platform skipped", func(t *testing.T) {
		content := `FROM --platform=linux/amd64 golang:1.26 AS base
FROM --platform=linux/arm64 base AS cross`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})

	t.Run("multi-stage with stage alias reuse", func(t *testing.T) {
		content := `FROM python:3.14-alpine@sha256:abc AS base
FROM base AS builder
COPY --from=ghcr.io/astral-sh/uv:0.11@sha256:def /uv /bin/uv
FROM base AS runtime
COPY --from=builder /app /app`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		assert.Equal(t, "python:3.14-alpine", refs[0].TagRef)
		assert.Equal(t, "ghcr.io/astral-sh/uv:0.11", refs[1].TagRef)
	})

	t.Run("FROM numeric stage index skipped", func(t *testing.T) {
		content := `FROM golang:1.26
FROM 0`
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, "golang:1.26", refs[0].TagRef)
	})
}

func TestParseDockerfile_offsets(t *testing.T) {
	t.Run("simple FROM", func(t *testing.T) {
		content := "FROM golang:1.26-alpine"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, 5, refs[0].Start)
		assert.Equal(t, refs[0].Original, content[refs[0].Start:refs[0].Start+len(refs[0].Original)])
	})

	t.Run("continuation line", func(t *testing.T) {
		content := "FROM --platform=linux/amd64 \\\n    golang:1.26-alpine AS builder"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 1)
		assert.Equal(t, refs[0].Original, content[refs[0].Start:refs[0].Start+len(refs[0].Original)])
	})

	t.Run("COPY --from and RUN --mount offsets", func(t *testing.T) {
		content := "FROM golang:1.26 AS builder\nCOPY --from=ghcr.io/org/tool:1.0@sha256:def /bin/tool /bin/tool"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 2)
		for _, ref := range refs {
			assert.Equal(t, ref.Original, content[ref.Start:ref.Start+len(ref.Original)])
		}
	})

	t.Run("multi-stage offsets", func(t *testing.T) {
		content := "FROM golang:1.26-alpine@sha256:aaa AS builder\nFROM gcr.io/distroless/static@sha256:bbb AS runtime\nCOPY --from=ghcr.io/org/check:1.0@sha256:ccc /bin/check /bin/check"
		refs := ParseDockerfile(content)
		require.Len(t, refs, 3)
		for _, ref := range refs {
			assert.Equal(t, ref.Original, content[ref.Start:ref.Start+len(ref.Original)],
				"offset mismatch for %s", ref.Original)
		}
	})
}

func TestIsStageRef(t *testing.T) {
	stages := map[string]bool{"builder": true, "runtime": true}

	assert.True(t, isStageRef("builder", stages))
	assert.True(t, isStageRef("0", stages))
	assert.True(t, isStageRef("1", stages))
	assert.False(t, isStageRef("golang:1.26", stages))
	assert.False(t, isStageRef("ghcr.io/org/img:1.0", stages))
	assert.False(t, isStageRef("", stages))
}

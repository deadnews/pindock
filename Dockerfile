FROM --platform=${BUILDPLATFORM} golang:1.26.4-alpine@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f AS builder

ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOARCH=${TARGETARCH} \
    GOFLAGS="-ldflags=-s" \
    GOCACHE="/cache/build" \
    GOMODCACHE="/cache/mod"

WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=${GOMODCACHE} \
    go mod download

COPY --parents cmd internal ./
RUN --mount=type=cache,target=${GOCACHE} \
    --mount=type=cache,target=${GOMODCACHE} \
    go build -o /bin/pindock ./cmd/pindock

FROM gcr.io/distroless/static@sha256:3592aa8171c77482f62bbc4164e6a2d141c6122554ace66e5cc910cadb961ff0 AS runtime

COPY --from=builder /bin/pindock /bin/pindock

USER nonroot:nonroot
HEALTHCHECK NONE

ENTRYPOINT ["/bin/pindock"]

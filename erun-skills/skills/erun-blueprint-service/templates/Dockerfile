# Stage 1: builder — provisions the toolchain, runs the tests, produces the
# artefact. Example shown is Go; swap the base image, WORKDIR contents, and
# RUN commands for another toolchain (node, python, java, ...) — the shape
# (toolchain -> test -> build) stays the same regardless of language, per
# erun's multi-stage Dockerfile convention.
FROM --platform=$BUILDPLATFORM golang:1.26.0 AS builder

WORKDIR /src
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH

# Tests that don't depend on a deployed artefact run here. A failure fails
# the build, so no image is produced from a red test run.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/__COMPONENT__ .

# Stage 2: runtime — thin, no build tools, non-root.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=builder --chmod=0755 /out/__COMPONENT__ /usr/local/bin/__COMPONENT__

USER erun
WORKDIR /home/erun

EXPOSE __PORT__

ENTRYPOINT ["__COMPONENT__"]

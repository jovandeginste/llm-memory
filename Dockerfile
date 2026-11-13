# syntax=docker/dockerfile:1

# Build stage. modernc.org/sqlite is pure Go, so the binary is fully static.
FROM golang:1.26-alpine AS build

WORKDIR /src

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/llmmem ./cmd/llmmem

# Runtime stage.
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 llmmem \
    && adduser -S -u 10001 -G llmmem -h /home/llmmem llmmem \
    && mkdir -p /data \
    && chown llmmem:llmmem /data

COPY --from=build /out/llmmem /usr/local/bin/llmmem

ENV HOME=/home/llmmem \
    LLM_MEMORY_DATA=/data \
    LLM_MEMORY_MCP_ADDR=0.0.0.0:8088

USER llmmem

VOLUME ["/data"]
EXPOSE 8088

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8088/healthz || exit 1

ENTRYPOINT ["llmmem"]
CMD ["mcp"]

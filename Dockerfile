# Build stage
FROM golang:1.22-alpine AS build

WORKDIR /src

# Cache deps
COPY go.mod ./
COPY go.sum* ./
RUN go mod download

# Copy source
COPY src/ ./src/
COPY go.mod ./
COPY go.sum* ./

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -extldflags=-static -X main.version=${IMAGE_VERSION:-dev}" -o /out/github-copilot-svcs ./src

# Runtime stage
FROM alpine:3.20 AS runtime

RUN adduser -D -u 10001 appuser \
    && apk add --no-cache ca-certificates curl su-exec

WORKDIR /app

# Copy binary
COPY --from=build /out/github-copilot-svcs /app/github-copilot-svcs
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod 0755 /app/entrypoint.sh

# Fixed CODEX_HOME so both `docker exec` (root) and the service (appuser) use the same path
ENV CODEX_HOME=/app/.codex
RUN mkdir -p /app/.codex && chown appuser:appuser /app/.codex && chmod 0700 /app/.codex

USER root

EXPOSE 7071

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -fsSL http://localhost:7071/health || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["run"]

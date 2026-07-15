FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY src/ ./src/

ARG IMAGE_VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-s -w -extldflags=-static -X github.com/duvu/ya-router/src.version=${IMAGE_VERSION}" \
      -o /out/ya-router ./cmd/ya-router

FROM alpine:3.20 AS runtime

RUN adduser -D -u 10001 appuser \
    && apk add --no-cache ca-certificates curl su-exec

WORKDIR /app
COPY --from=build /out/ya-router /app/ya-router
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod 0755 /app/entrypoint.sh /app/ya-router

USER root
EXPOSE 7071

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -fsSL http://127.0.0.1:7071/health/live || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["run"]

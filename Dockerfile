# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pantara-api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pantara-migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pantara-seed ./cmd/seed

FROM alpine:3.21 AS runner
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl && \
    adduser -D -H -u 10001 pantara

COPY --from=builder /out/pantara-api /app/pantara-api
COPY --from=builder /out/pantara-migrate /app/pantara-migrate
COPY --from=builder /out/pantara-seed /app/pantara-seed

USER pantara
EXPOSE 8000

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
  CMD sh -c 'curl -sf "http://127.0.0.1:${PORT:-8000}/healthz" || exit 1'

CMD ["/app/pantara-api"]

# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS builder
WORKDIR /src

# Dependencies first so they stay cached when only source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Release builds pass the git tag; anything else reports dev.
ARG VERSION=dev
# CGO stays off: modernc.org/sqlite is pure Go, so this produces a static binary
# with no libc dependency in the final image.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/yuna .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 yuna && \
    mkdir -p /data && chown yuna:yuna /data

COPY --from=builder /out/yuna /usr/local/bin/yuna

USER yuna
VOLUME ["/data"]

ENV YUNA_DB_PATH=/data/yuna.db \
    YUNA_LOG_FILE=/data/yuna.log

ENTRYPOINT ["/usr/local/bin/yuna"]

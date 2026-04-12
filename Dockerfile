FROM golang:1.22-alpine AS builder

ARG VERSION=dev

WORKDIR /build

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY config/ ./config/
COPY context/ ./context/
COPY dialect/ ./dialect/
COPY memory/ ./memory/
COPY stream/ ./stream/
COPY tool/ ./tool/
COPY types/ ./types/
COPY vault/ ./vault/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/ghyll ./cmd/ghyll
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /bin/ghyll-vault ./cmd/ghyll-vault

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata git

RUN addgroup -g 1000 ghyll && \
    adduser -u 1000 -G ghyll -s /bin/sh -D ghyll

WORKDIR /app

COPY --from=builder /bin/ghyll /app/
COPY --from=builder /bin/ghyll-vault /app/

USER ghyll

ENTRYPOINT ["/app/ghyll"]

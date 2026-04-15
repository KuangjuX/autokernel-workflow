# ---- Build stage ----
FROM golang:1.22 AS builder

WORKDIR /app

# Configure Go proxy for internal dependencies (override at build time if needed)
ARG GOPROXY="https://goproxy.cn,direct"
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# go-sqlite3 requires CGO; statically link libc via musl for a portable binary
RUN apt-get update && apt-get install -y --no-install-recommends musl-tools && rm -rf /var/lib/apt/lists/*
RUN CC=musl-gcc CGO_ENABLED=1 go build \
    -ldflags '-linkmode external -extldflags "-static"' \
    -o kernelhub ./cmd/kernelhub

# ---- Runtime stage ----
FROM ubuntu:22.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl tzdata && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/kernelhub .

RUN mkdir -p /data

EXPOSE 8080

VOLUME ["/data"]

ENTRYPOINT ["./kernelhub"]
CMD ["server", "--listen", "0.0.0.0:8080", "--db-path", "/data/history.db"]

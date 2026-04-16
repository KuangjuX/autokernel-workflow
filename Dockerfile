# ---- Build stage ----
FROM golang:1.22-bookworm AS builder

WORKDIR /app

ARG GOPROXY="https://goproxy.cn,direct"
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc libc6-dev && \
    rm -rf /var/lib/apt/lists/*

RUN CGO_ENABLED=1 go build -o kernelhub ./cmd/kernelhub

# ---- Runtime stage ----
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl net-tools vim tzdata && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/kernelhub .

EXPOSE 8080

VOLUME ["/data"]

ENTRYPOINT ["./kernelhub"]
CMD ["serve", "--listen", "0.0.0.0:8080", "--db-path", "/data/history.db"]

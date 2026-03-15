# Build stage
FROM golang:1.22 AS builder

WORKDIR /app

ARG GOPROXY=https://goproxy.io,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# Copy go module files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary (verbose to see errors)
RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o video-subscribe-dl ./cmd/server

# Runtime stage — alpine for smaller image (~80MB vs ~200MB debian)
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ffmpeg curl ca-certificates tzdata \
    && ffmpeg -version | head -1

# Copy binary and static files
COPY --from=builder /app/video-subscribe-dl .
COPY --from=builder /app/web ./web

# Create data dirs
RUN mkdir -p /app/data /app/downloads

VOLUME ["/app/data", "/app/downloads"]

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s \
    CMD curl -f http://localhost:8080/health || exit 1

EXPOSE 8080

ENTRYPOINT ["./video-subscribe-dl"]
CMD ["--data-dir", "/app/data", "--download-dir", "/app/downloads", "--port", "8080"]

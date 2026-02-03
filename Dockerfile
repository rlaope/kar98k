# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
ARG VERSION=dev
ARG BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o /kar98k ./cmd/kar98k

# Runtime stage
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' kar98k
USER kar98k

WORKDIR /app

# Copy binary from builder
COPY --from=builder /kar98k /app/kar98k

# Copy default config
COPY configs/kar98k.yaml /app/configs/kar98k.yaml

# Expose metrics port
EXPOSE 9090

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9090/healthz || exit 1

ENTRYPOINT ["/app/kar98k"]
CMD ["-config", "/app/configs/kar98k.yaml"]

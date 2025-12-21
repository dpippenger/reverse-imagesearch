# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o imgsearch ./cmd/imgsearch

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS (if needed in future)
RUN apk --no-cache add ca-certificates

# Create non-root user for security
RUN addgroup -S imgsearch && adduser -S imgsearch -G imgsearch

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/imgsearch .

# Change ownership to non-root user
RUN chown -R imgsearch:imgsearch /app

USER imgsearch

# Expose web UI port
EXPOSE 9183

# Default to web mode, binding to all interfaces for container access
ENTRYPOINT ["./imgsearch"]
CMD ["-web", "-bind", "0.0.0.0"]

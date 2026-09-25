# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Install git (needed for some Go modules)
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags "-X github.com/AbhishekCS3459/find-me-backend/internal/platform/version.Version=${VERSION}" \
    -o api ./cmd/api

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/api .

# Expose port
EXPOSE 8080

# Run the application
CMD ["./api"]


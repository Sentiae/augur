# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy local replace dependencies (from parent dir)
COPY platform-kit/ /platform-kit/
COPY infrastructure-intelligence-service/ /infrastructure-intelligence-service/

WORKDIR /infrastructure-intelligence-service

# Download dependencies
RUN go mod download

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Runtime stage
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /server /app/server
COPY --from=builder /infrastructure-intelligence-service/configs /app/configs

EXPOSE 8089 50059

LABEL org.opencontainers.image.source="https://github.com/sentiae/infrastructure-intelligence-service"

USER nonroot:nonroot

ENTRYPOINT ["/app/server"]

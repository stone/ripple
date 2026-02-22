# Build stage
FROM golang:1 AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ripple .

# Runtime stage - distroless
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /app/ripple /ripple
COPY --from=builder /app/config.example.yaml /config.yaml

# Default to server mode on port 8080
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/ripple"]
CMD ["-c", "/config.yaml", "-serve", ":8080"]

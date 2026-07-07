# --- Stage 1: Build the Go Binary ---
FROM golang:1.26-alpine AS builder

# Install git and certificates (required to fetch Go modules and make HTTPS requests)
RUN apk add --no-cache git ca-certificates

# Set the working directory inside the container
WORKDIR /app

# Copy dependency files first to leverage Docker caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the binary statically 
# CGO_ENABLED=0 ensures it doesn't rely on dynamic C libraries (perfect for Alpine/Scratch)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o ddns-updater .

# --- Stage 2: Final Lightweight Image ---
FROM alpine:3.20

# Install CA certificates so the app can securely connect to api.ipify.org and Cloudflare
RUN apk add --no-cache ca-certificates

WORKDIR /root/

# Copy the compiled binary from the builder stage
COPY --from=builder /app/ddns-updater .

# Run the application
CMD ["./ddns-updater"]
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the entire project
COPY . .

# Build the application from the correct path
RUN CGO_ENABLED=1 GOOS=linux go build -o reading-list-api ./cmd/api/main.go

FROM alpine:latest

# Install SQLite and necessary libs
RUN apk add --no-cache sqlite libc6-compat ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /build/reading-list-api .

# Copy env file
COPY .env* ./

# Create data directory for SQLite
RUN mkdir -p /app/data

# Expose the port the app runs on
EXPOSE 8080

# Command to run the executable
CMD ["./reading-list-api"]
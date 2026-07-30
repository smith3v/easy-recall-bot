# Dockerfile

# First stage: Build the tg-word-reminder binary
FROM golang:1.26.5 AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum  ./

# Download dependencies
RUN go mod download

# Copy the rest of the application code
COPY . .

# Build the runtime and one-shot administration binaries
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tg-word-reminder ./cmd/tg-word-reminder
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o notify-legacy-users ./cmd/notify-legacy-users

# Second stage: Create a minimal image to run the binary
FROM alpine:latest

LABEL org.opencontainers.image.source=https://github.com/smith3v/tg-word-reminder

# Set the working directory
WORKDIR /app

# Copy the binaries from the builder stage
COPY --from=builder /app/tg-word-reminder .
COPY --from=builder /app/notify-legacy-users .
COPY --from=builder /app/vocabularies /app/vocabularies

# Command to run the bot
CMD ["/app/tg-word-reminder"]

# Note: The config.json file should be mounted at runtime

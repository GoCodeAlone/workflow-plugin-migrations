# syntax=docker/dockerfile:1
# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=amd64 GOOS=linux \
    go build -ldflags="-s -w" -o /workflow-migrate ./cmd/workflow-migrate

# Runtime stage — distroless/static has no shell, minimal attack surface
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workflow-migrate /workflow-migrate
ENTRYPOINT ["/workflow-migrate"]

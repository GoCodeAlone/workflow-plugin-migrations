# syntax=docker/dockerfile:1
# Build stage — Go version pinned to match go.mod exactly; bump here if go.mod is updated
FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine AS builder
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=$TARGETARCH GOOS=linux \
    go build -ldflags="-s -w" -o /workflow-migrate ./cmd/workflow-migrate

# Runtime stage — distroless/static has no shell, minimal attack surface
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workflow-migrate /workflow-migrate
ENTRYPOINT ["/workflow-migrate"]

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-04-24

### Added

- **Official `workflow-migrate` Docker image published to GHCR** — each release now builds and pushes `ghcr.io/gocodealone/workflow-migrate:{version}` (multi-arch: linux/amd64 + linux/arm64). Pre-release tags (containing `-`) are versioned only; stable releases also push `:latest`. Image uses the existing distroless/static base with a non-root user and no shell. BMW and other consumers can reference the image directly without cloning and building from source.

## [0.2.0] - 2026-04-15

### Added

- Atlas driver (`workflow-plugin-atlas-migrate`) for schema-as-code migrations
- `workflow-atlas-migrate` lint tool standalone binary
- Atlas plugin binary and release artifacts
- `cmd/workflow-migrate/Dockerfile` for building standalone migration runner image

## [0.1.0] - 2026-04-01

### Added

- Initial release: `workflow-plugin-migrations` plugin binary
- golang-migrate and goose drivers
- Module types and pipeline steps
- Conformance test suite
- `workflow-migrate` standalone binary for pre-deploy jobs

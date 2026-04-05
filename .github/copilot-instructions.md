# Project Guidelines

## Build and Test

- Use Go 1.24.x to match CI in [../.github/workflows/ci.yml](../.github/workflows/ci.yml).
- Validate changes with `go test ./...`. For targeted work, prefer focused packages first such as `go test ./internal/app ./internal/store`.
- Build the service with `go build ./...` or `go build -o bin/mimic-sync2ch.exe ./cmd/mimic-sync2ch`.
- Run locally with `go run ./cmd/mimic-sync2ch`. Set `MIMIC_SYNC2CH_USER` and `MIMIC_SYNC2CH_PASSWORD` before starting; other runtime settings are documented in [../README.md](../README.md) and [../.env.example](../.env.example).

## Architecture

- [../cmd/mimic-sync2ch/main.go](../cmd/mimic-sync2ch/main.go) is the entrypoint. It loads config, opens the SQLite store, starts the HTTP server, and handles shutdown.
- [../internal/app/server.go](../internal/app/server.go) owns HTTP routing, Basic authentication, gzip handling, XML decoding/encoding, and sync response assembly.
- [../internal/app/admin.go](../internal/app/admin.go) contains the admin UI and deletion flow.
- [../internal/store/sqlite.go](../internal/store/sqlite.go) is the source of truth for snapshot replacement, sync counters, tombstones, and schema migration.
- [../internal/syncxml/types.go](../internal/syncxml/types.go) defines the sync2ch-compatible XML model. Keep API changes aligned with these types and the existing tests.

## Conventions

- Preserve sync2ch compatibility behaviors already covered by tests instead of simplifying them.
- `ReplaceSnapshot` is not a naive overwrite. Preserve monotonic fields per URL, keep stored titles when an incoming title is empty, and keep deletion tombstones so stale clients cannot resurrect removed threads.
- Response thread status values matter: this codebase uses sync2ch v3 style `a`, `u`, and `n`, and `n` responses intentionally omit sync attributes other than the URL.
- Daily sync limiting is part of the contract. `remain` is the remaining count for the current day, and requests beyond the limit must return HTTP 403.
- Both `/api/sync` and `/api/sync3` are supported and should stay behaviorally aligned unless a task explicitly changes compatibility.
- When touching HTTP or storage behavior, update or add tests in [../internal/app/server_test.go](../internal/app/server_test.go) and [../internal/store/sqlite_test.go](../internal/store/sqlite_test.go) alongside the code change.

## References

- Use [../README.md](../README.md) for setup, environment variables, runtime examples, and compatibility notes.

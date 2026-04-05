# MonaSync （　´∀｀）

A server for personal use that reimplements a minimal compatible sync2ch API in Go.

Its purpose is to imitate the XML-based sync2ch API expected by clients like ChMate and Siki, allowing individuals to run their own servers and continue syncing their favorites.

## Principles

- MIT License
- Operates as a single Go binary
- State management with a single SQLite file
- Implements a minimal sync2ch-compatible API and admin UI
- v1 uses snapshot syncing

## Current Implementation Scope

- Basic Authentication
- Display and deletion of saved data via the admin UI
- POST /api/sync
- POST /api/sync3
- Saving favorites lists to SQLite
- Returning saved lists as XML
- GET /
- GET /healthz

Not yet implemented:

- Multi-user management UI
- Rate limiting
- HTTPS termination
- Detailed conflict resolution

## Operation Model

Saves the list of favorites sent from the client as the latest snapshot for that user.

It then returns all saved threads for that same user in XML format. The client can also detect local deletions by comparing differences with the returned list.

To avoid rolling back, `read`, `now`, and `count` adopt the maximum values independently per URL.

Sync counts are managed on a daily basis per user and will succeed up to 99 times a day by default. Requests exceeding the limit will return a 403.

## Requirements

- Go 1.24 or higher

## Configuration

Configuration is done using environment variables. Please refer to [.env.example](.env.example) for an example.

- MONASYNC_ADDR: Listening address. Default is `:8081`
- MONASYNC_DB_PATH: SQLite file path. Default is `./data/monasync.db`
- MONASYNC_USER: Basic authentication username
- MONASYNC_PASSWORD: Basic authentication password
- MONASYNC_DAILY_LIMIT: Daily limit for sync count. Default is `99`

The module name in `go.mod` is matched with the GitHub repository URL.

```go
module github.com/romtenma/monasync
```

Import example:

```go
import "github.com/romtenma/monasync/internal/app"
```

## Example Usage

```powershell
$env:MONASYNC_USER = "admin"
$env:MONASYNC_PASSWORD = "change-me"
go run ./cmd/monasync
```

If you want to log received and sent XML data, add the following flag on startup:

```powershell
go run ./cmd/monasync --dump-xml
```

After starting, if you open http://127.0.0.1:8081/ in your browser, you can see the list of currently saved threads after basic authentication.

You can delete threads individually in the admin UI. Doing so updates the tombstone too, making it difficult for deleted data to resurface during syncing with older clients.

## Example Build

```powershell
go build -o bin/monasync.exe ./cmd/monasync
```

## Siki Configuration Example

Point the `apiurl` to this server using configuration equivalent to `sync2ch_setting.js`.

```js
{
  user: "admin",
  password: "change-me",
  appname: "Siki",
  apiurl: "http://127.0.0.1:8081/api/sync"
}
```

Both `/api/sync` and `/api/sync3` are accepted. You should change the URL to the production URL when going public.

## API Compatibility Notes

Received:

- The root element is `sync2ch_request`
- `entities/th` holds the thread entity
- `thread_group/dir` handles grouping via `id_list`

Returned:

- The root element is `sync2ch_response`
- `result`, `client_id`, `sync_number`, and `remain` are returned as attributes
- `thread_group/dir/th` returns group references
- `entities/th` returns URL, title, and read position

## GitHub Publishing Checklist

- Set the Description and Topics for the GitHub repository
- Enable Actions and confirm that CI passes
- If necessary, add HTTPS termination or reverse proxy configurations to the README

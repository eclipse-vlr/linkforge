# LinkForge

**LinkForge** is a URL shortener backend written in Go. It provides an HTTP API for creating short links and redirecting users to original URLs. PostgreSQL is used for persistent storage.

The project is built to practice backend development with Go, REST API design, database integration, error handling, and automated testing.

## Features

- Create short links through a REST API
- Generate random 8-character codes using `crypto/rand`
- Validate URLs and allow only HTTP and HTTPS schemes
- Redirect users to original URLs
- Store links in PostgreSQL
- Handle duplicate short codes with retry logic
- Use a repository interface to separate HTTP handlers from storage
- Apply request-scoped timeouts to database operations
- Provide a health check endpoint
- Write structured JSON logs to stdout using `log/slog`
- Log HTTP request method, path, status, and duration through middleware
- Gracefully stop HTTP requests before closing the PostgreSQL pool
- Test HTTP handlers and error scenarios

## Tech stack

- **Language:** Go
- **HTTP:** `net/http`
- **Database:** PostgreSQL
- **PostgreSQL driver:** `pgx/v5`
- **Containers:** Docker Compose
- **Testing:** Go standard library (`testing`, `httptest`)

## Architecture

The application uses a repository interface to separate HTTP handling from data storage.

```text
HTTP Client
    |
    v
HTTP Router and Handlers
    |
    v
LinkRepository interface
    |
    v
PostgresStore
    |
    v
PostgreSQL
```

The `LinkRepository` interface defines the operations required by the HTTP handlers. The PostgreSQL implementation handles database queries and maps database errors to application-level errors.

## API

### Health check

`GET /healthz`

Returns the service health status.

Example response:

```json
{
  "status": "ok"
}
```

### Create a short link

`POST /api/v1/links`

Request body:

```json
{
  "url": "https://example.com/articles/go"
}
```

Example response (`201 Created`):

```json
{
  "code": "XmrHIdYe",
  "short_url": "http://localhost:8080/XmrHIdYe",
  "original_url": "https://example.com/articles/go"
}
```

The generated code in this example is illustrative; each request can produce a different code.

Only absolute HTTP and HTTPS URLs are accepted.
The JSON body is limited to 16 KiB (`413` if exceeded), and the trimmed URL
to 4096 bytes (`400` if exceeded). The body must contain exactly one JSON object;
unknown fields, extra JSON values, and trailing non-whitespace data return `400`.

### Redirect to the original URL

`GET /{code}`

Redirects to the original URL associated with the supplied code.

A successful redirect returns `302 Found` and a `Location` header containing the original URL.

### Link statistics

`GET /api/v1/links/{code}/stats`

Returns `200 OK` with the link code and redirect attempt count:

```json
{"code":"abc12345","click_count":12}
```

Unknown codes return `404`; reading statistics does not increment the counter.
Statistics are public to anyone who knows the code. Repeated requests and bots
are counted, and a redirect does not confirm arrival at the destination.

### Error responses

| Status | Meaning |
|---|---|
| `201 Created` | Short link created |
| `302 Found` | Redirect to original URL |
| `400 Bad Request` | Invalid JSON or URL |
| `404 Not Found` | Short code does not exist |
| `405 Method Not Allowed` | Unsupported HTTP method |
| `413 Content Too Large` | Request body exceeds 16 KiB |
| `500 Internal Server Error` | Internal or storage error |

## Getting started

### Prerequisites

Install the following tools:

- Go
- Docker Desktop with Docker Compose
- Git

### 1. Clone the repository

```bash
git clone https://github.com/eclipse-vlr/linkforge.git
cd linkforge
```

### 2. Start PostgreSQL

```bash
docker compose up -d db
```

Check the container status:

```bash
docker compose ps
```

### 3. Create the database table

LinkForge uses [golang-migrate](https://github.com/golang-migrate/migrate) to manage PostgreSQL schema changes.

SQL migration files are stored in the `migrations/` directory:

- `000001_create_links_table.up.sql` — creates the `links` table.
- `000001_create_links_table.down.sql` — removes the `links` table.
- `000002_add_click_count.up.sql` — adds a click counter initialized to zero for existing and new links.
- `000002_add_click_count.down.sql` — removes the counter and its accumulated statistics, preserving links.

Migrations are applied automatically when the application starts.
Startup migrations have a total 30-second deadline, including connecting,
authentication, waiting for locks, and running SQL. Ctrl+C/SIGTERM also cancels
this work by closing migration connections. A timeout or cancellation stops
startup. If a migration was interrupted after being marked dirty, inspect the
schema and migration history before repairing it; LinkForge never forces a version.

On the first startup, LinkForge creates the required database tables. On subsequent startups, previously applied migrations are skipped.

No manual database initialization is required.

**Note:** Down migrations may delete existing data and should be used with caution.

### 4. Start the application

The default database connection string is configured for local development.

```bash
go mod download
go run ./cmd/api
```

The HTTP server starts on port `8080`.
It allows 5 seconds for request headers, 10 seconds for the entire request read,
15 seconds for response writes, and 60 seconds for idle keep-alive connections.

The application applies pending database migrations before starting the HTTP server.

Expected startup output:

```text
{"time":"2026-10-13T12:00:00+03:00","level":"INFO","msg":"database migrations applied successfully"}
{"time":"2026-10-13T12:00:00+03:00","level":"INFO","msg":"HTTP server started","addr":"[::]:8080"}
```

Run the application from the repository root because migration files are currently loaded using a relative path.

To stop the application, press Ctrl+C (or send SIGTERM on Linux). The server
stops accepting connections and waits up to `SHUTDOWN_TIMEOUT` (10 seconds by default) for active requests.
If the deadline expires, it closes the remaining connections and reports an
error. The PostgreSQL pool is closed after HTTP shutdown. For deployment, run
the compiled binary directly so it receives process signals.

### 5. Test the API

Check the health endpoint:

```powershell
Invoke-RestMethod http://localhost:8080/healthz
```

Create a short link:

```powershell
$body = @{
    url = "https://example.com/articles/go"
} | ConvertTo-Json

$response = Invoke-RestMethod `
    -Uri http://localhost:8080/api/v1/links `
    -Method Post `
    -ContentType "application/json" `
    -Body $body

$response
```

Open the returned `short_url` in a browser to test the redirect.

### 6. Run tests

```bash
go test ./...
```

To also test the statistics repository against a local PostgreSQL instance:

```powershell
$env:TEST_DATABASE_URL = "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable"
go test -count=1 ./...
```

The statistics test creates and removes an isolated schema and requires schema
creation privileges. It checks migration rollback and concurrent increments.
Without `TEST_DATABASE_URL`, this database test is skipped.

## Database schema

The `links` table stores the original URL, its short code, and the creation timestamp.

| Column | Type | Description |
|---|---|---|
| `id` | `BIGINT` | Primary key |
| `code` | `VARCHAR(16)` | Unique short code |
| `original_url` | `TEXT` | Original URL |
| `created_at` | `TIMESTAMPTZ` | Creation timestamp |
| `click_count` | `BIGINT` | Click counter, defaults to zero |

The unique constraint on `code` prevents duplicate short codes from being stored.

Each GET request for an existing short code attempts to increment the counter
before returning a redirect. Repeated requests and bots are counted; this does
not confirm that the destination page was opened. Counter updates have a 500ms
timeout. An update failure is logged and does not prevent the redirect, so counts
may be lower during failures. Use `GET /api/v1/links/{code}/stats` to read the counter.


### Migration versioning

Migration history is tracked in the `schema_migrations` table.

Each migration has a version number and can contain two SQL scripts:

- `up` applies a schema change.
- `down` reverts a schema change.

The migration system ensures that already applied migrations are not executed again.

## Configuration

The application reads configuration from environment variables at startup.
Unset or empty variables use the defaults below. No `.env` file is loaded automatically.

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listening address in `host:port` format; port 1–65535 |
| `BASE_URL` | `http://localhost:8080` | Public HTTP/HTTPS origin used to generate short links |
| `DATABASE_URL` | `postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable` | PostgreSQL connection URL |
| `SHUTDOWN_TIMEOUT` | `10s` | Positive Go duration for HTTP shutdown, e.g. `500ms` or `15s` |
| `REDIS_URL` | empty (disabled) | Optional Redis URL, e.g. `redis://localhost:6379/0` or `rediss://host:6379/0` for TLS |
| `REDIS_TIMEOUT` | `100ms` | Positive duration limiting Redis operations |
| `REDIS_CACHE_TTL` | `10m` | Cache lifetime, at least `1ms` |

Configuration is checked before migrations and database connections. `BASE_URL`
must have no credentials, path prefix, query, or fragment; a trailing `/` is
removed. `HTTP_ADDR` and `BASE_URL` are independent: behind a reverse proxy, the
listening address may be internal while short links use a public HTTPS domain.
`DATABASE_URL` must use the `postgres` or `postgresql` scheme and include a hostname;
database availability and authentication are checked during startup.

Example for local development:

```powershell
$env:DATABASE_URL = "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable"
$env:HTTP_ADDR = ":9090"
$env:BASE_URL = "http://localhost:9090"
$env:SHUTDOWN_TIMEOUT = "15s"
go run ./cmd/api
```

The Compose setup is for local development: PostgreSQL and Redis ports bind only
to `127.0.0.1`. After changing a port binding, run `docker compose up -d db` to
recreate the container with the new binding; the database volume is retained.
The default credentials are intended for local development only. The Compose
PostgreSQL user is an initialization superuser. For deployment, use private
database networking, unique secrets and a dedicated non-superuser application
role; run schema migrations separately with a role permitted to change the schema.
Do not deploy the local Compose credentials or superuser application connection.

CI checks reachable dependency vulnerabilities with `govulncheck`. To run the
same check locally: `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.

### Redis connection setup

```powershell
docker compose up -d --wait redis
$env:REDIS_URL = "redis://localhost:6379/0"
go run ./cmd/api
```

Redis is checked with `PING` at startup. Redirects first look up
`linkforge:link:{code}` in Redis. On a miss or cache error, the URL is read from
PostgreSQL and cached for `REDIS_CACHE_TTL`. Unknown codes are not cached.
Cache failures are logged without preventing redirects. The client stays enabled
after a failed startup check so later requests can use Redis when it recovers.
A malformed Redis configuration prevents startup.
The client is closed after HTTP shutdown. Its operations respect context deadlines,
and automatic command retries are disabled to keep fallback delays bounded.

The local Redis service exposes port 6379 on loopback only and does not persist
data: it holds disposable cache entries. Cache hits still update click statistics
in PostgreSQL. Creation and statistics reads also always use PostgreSQL.

To verify the real connection and client cleanup:

```powershell
$env:TEST_REDIS_URL = "redis://localhost:6379/0"
go test -count=1 -run TestRedis ./cmd/api
```

## Project structure

```text
linkforge/
├── cmd/
│   └── api/
│       ├── main.go
│       ├── main_test.go
│       └── postgres_store.go
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

## Future improvements

- Add database migrations
- Add request IDs to correlate HTTP request logs and errors
- Add integration tests using PostgreSQL
- Add GitHub Actions for automated testing
- Add link expiration and usage statistics

## License

No license has been specified yet.

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

### Redirect to the original URL

`GET /{code}`

Redirects to the original URL associated with the supplied code.

A successful redirect returns `302 Found` and a `Location` header containing the original URL.

### Error responses

| Status | Meaning |
|---|---|
| `201 Created` | Short link created |
| `302 Found` | Redirect to original URL |
| `400 Bad Request` | Invalid JSON or URL |
| `404 Not Found` | Short code does not exist |
| `405 Method Not Allowed` | Unsupported HTTP method |
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

The application expects a `links` table in PostgreSQL. If the table has not been created yet, execute:

```powershell
docker exec -i linkforge-postgres psql -U linkforge -d linkforge -c "CREATE TABLE IF NOT EXISTS links (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, code VARCHAR(16) UNIQUE NOT NULL, original_url TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());"
```

### 4. Start the application

The default database connection string is configured for local development.

```bash
go mod download
go run ./cmd/api
```

The HTTP server starts on port `8080`.

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

## Database schema

The `links` table stores the original URL, its short code, and the creation timestamp.

| Column | Type | Description |
|---|---|---|
| `id` | `BIGINT` | Primary key |
| `code` | `VARCHAR(16)` | Unique short code |
| `original_url` | `TEXT` | Original URL |
| `created_at` | `TIMESTAMPTZ` | Creation timestamp |

The unique constraint on `code` prevents duplicate short codes from being stored.

## Configuration

The application reads the database connection string from the `DATABASE_URL` environment variable.

Example for local development:

```powershell
$env:DATABASE_URL = "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable"
go run ./cmd/api
```

The default credentials are intended for local development only. Use appropriate secrets and database security settings in a deployed environment.

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
- Add configurable base URL for generated short links
- Add structured logging
- Add graceful HTTP server shutdown
- Add integration tests using PostgreSQL
- Add GitHub Actions for automated testing
- Add request size limits and stricter JSON validation
- Add link expiration and usage statistics

## License

No license has been specified yet.
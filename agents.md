# AGENTS.md

## Project Overview

**matrix2acrobits** is a Go-based proxy server bridging the Acrobits Mobile App (SIP Client) and a Matrix Server (hosted on NethServer 8).

The proxy translates Acrobits' proprietary HTTP API calls for messaging into standard Matrix Client-Server API calls, enabling Acrobits users to send and receive messages via a Matrix backend.

---

## Core Functionality

- **Authentication:** Uses the username and password from Acrobits requests to authenticate against the Matrix server and obtain an access token.
- **Sending Messages:** Translates Acrobits `/send_message` requests into Matrix `/rooms/{roomId}/send/...` events.
- **Fetching Messages:** Translates Acrobits `/fetch_messages` requests into Matrix `/sync` calls, filtering and formatting history to match Acrobits' expected JSON structure.

---

## Tech Stack & Tools

- **Language:** Golang (1.23+)
- **Web Framework:** Standard Echo (v4) framework
- **Matrix SDK:** `mautrix-go` (recommended)
- **Logging:** `zerolog` for structured logging with configurable levels
- **Testing:** Standard testing package with `testify/assert` and `testify/mock`

---

## Documentation & Specs

- **OpenAPI:** OpenAPI 3.0 `docs/openapi.yaml`, keep it up to date with implementation.
- **README:** Overview, setup instructions, usage examples in `README.md`, keep it updated.

---

## Setup & Commands

- **Install Dependencies:** `go mod tidy`
- **Run Tests:** `go test -v ./...`
- **Run Server:** `go run cmd/server/main.go`
- **Linting:** `golangci-lint run`

---

## Code Style & Conventions

- **Formatting:** Strictly follow `gofmt`
- **Error Handling:** Wrap errors with context using `fmt.Errorf("context: %w", err)`. Return 500 for internal errors, 4xx for bad requests.
- **Configuration:** Use environment variables for configuration (e.g., `MATRIX_HOMESERVER_URL`, `PROXY_PORT`)

---

## Project Structure

```
cmd/              # Main application entry point
internal/logger/  # Structured logging with configurable levels (DEBUG, INFO, WARNING, CRITICAL)
internal/api/     # HTTP handlers for Acrobits endpoints
internal/matrix/  # Matrix client wrapper and logic
internal/service/ # Business logic (translation between Acrobits <-> Matrix models)
pkg/models/       # Shared structs (Acrobits request/response objects)
```
---

## Build

```bash
# build (produces ./matrix2acrobits)
go build -o matrix2acrobits .
```
---

## Quick run

```bash
export MATRIX_HOMESERVER_URL="https://matrix.your-homeserver-name.com"
export SUPER_ADMIN_TOKEN="YOUR_SECURE_APPLICATION_SERVICE_TOKEN"
export PROXY_PORT=8080
export LOGLEVEL=INFO  # DEBUG, INFO, WARNING, or CRITICAL
./matrix2acrobits
```

---

## Testing Strategy

### Unit Tests

- Test `internal/service` logic using mocks for the Matrix client.
- Verify correct JSON marshalling/unmarshalling of Acrobits payloads.
- Verify mapping logic (e.g., Matrix timestamp → Acrobits RFC3339).

### Integration Tests

- Spin up a mock Matrix server based on container:
  ```
  cd test
  ./test.sh run
  ```
- Tear down after tests:
  ```
  ./test.sh stop
  ```

---

## Definition of Done

- Both `/fetch_messages` and `/send_message` endpoints are implemented according to the OpenAPI spec below.
- Authentication works dynamically (proxy does not store credentials; it uses them to auth with Matrix on every request or caches the session).
- `fetch_messages` correctly handles the `last_id` cursor to only return new messages.
- Standard Go tests (unit and integration) are passing.
- Code is linted and formatted using `gofmt` and `golangci-lint`.



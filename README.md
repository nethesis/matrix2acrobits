# Matrix to Acrobits Proxy

This service acts as a proxy between an Acrobits softphone client and a Matrix homeserver, allowing users to send and receive Matrix messages through an SMS-like interface.

The proxy is written in Go and uses the following key technologies:
- **Web Framework**: `github.com/labstack/echo/v4`
- **Matrix Client Library**: `maunium.net/go/mautrix`

The service authenticates to the Matrix homeserver as an **Application Service**, which grants it the ability to perform actions (like sending messages) on behalf of other Matrix users (impersonation).

## Quick Start

The proxy is configured via environment variables. Minimal required env:

- `MATRIX_HOMESERVER_URL`: URL of your Matrix homeserver (e.g. `https://matrix.example`)
- `SUPER_ADMIN_TOKEN`: the Application Service `as_token` from your registration file
- `PROXY_PORT` (optional): port to listen on (default: `8080`)
- `AS_USER_ID` (optional): the user ID of the Application Service bot (default: `@_acrobits_proxy:matrix.example`)
- `LOGLEVEL` (optional): logging verbosity level - `DEBUG`, `INFO`, `WARNING`, `CRITICAL` (default: `INFO`)
- `MAPPING_FILE` (optional): path to a JSON file containing SMS-to-Matrix mappings to load at startup

Building and running

```bash
# build (produces ./matrix2acrobits)
go build -o matrix2acrobits .

# run (example)
export MATRIX_HOMESERVER_URL="https://matrix.your-homeserver-name.com"
export SUPER_ADMIN_TOKEN="YOUR_SECURE_APPLICATION_SERVICE_TOKEN"
export PROXY_PORT=8080
export AS_USER_ID="@_acrobits_proxy:your-homeserver-name.com"
export LOGLEVEL=INFO
./matrix2acrobits
```

### Logging Levels

The `LOGLEVEL` environment variable controls the verbosity of application logs:

- **DEBUG**: Detailed information for diagnosing issues (shows all API calls, mapping lookups, Matrix operations)
- **INFO**: General informational messages (successful operations, server startup) - **Default**
- **WARNING**: Warning messages for potentially problematic situations
- **CRITICAL**: Only critical errors

For debugging mapping and API issues, set `LOGLEVEL=DEBUG` to see detailed trace information.

### Loading Mappings from File

You can pre-load SMS-to-Matrix mappings at startup by providing a `MAPPING_FILE` environment variable pointing to a JSON file. This is useful for initializing the proxy with a set of known mappings.

The JSON file should be an array of mapping objects:

```json
[
  {
    "sms_number": "91201",
    "matrix_id": "@giacomo:synapse.gs.nethserver.net",
    "room_id": "!giacomo-room:synapse.gs.nethserver.net"
  },
  {
    "sms_number": "91202",
    "matrix_id": "@mario:synapse.gs.nethserver.net",
    "room_id": "!mario-room:synapse.gs.nethserver.net"
  }
]
```

Usage:

```bash
export MAPPING_FILE="/path/to/mappings.json"
./matrix2acrobits
```

The loaded mappings will be logged at startup with the message: `mappings loaded from file count=N file=/path/to/mappings.json`

## Extra info

- [Deploying with NS8](docs/DEPLOY_NS8.md)
- [OpenAPI Specification](docs/openapi.yaml)
- [Container Build & Usage](docs/CONTAINER.md)
- [Testing](test/README.md)


## Acrobits documentation

Implemented APIs:

- https://doc.acrobits.net/api/client/fetch_messages_modern.html
- https://doc.acrobits.net/api/client/send_message.html

## TODO

The following features are not yet implemented:

- implement password validation on send messages, currently the password is ignored
- implement https://doc.acrobits.net/api/client/push_token_reporter.html
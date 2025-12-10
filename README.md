# Matrix to Acrobits Proxy

This service acts as a proxy between an Acrobits softphone client and a Matrix homeserver, allowing users to send and receive Matrix messages through an -like interface.

The proxy is written in Go and uses the following key technologies:
- **Web Framework**: `github.com/labstack/echo/v4`
- **Matrix Client Library**: `maunium.net/go/mautrix`

The service authenticates to the Matrix homeserver as an **Application Service**, which grants it the ability to perform actions (like sending messages) on behalf of other Matrix users (impersonation).

## Quick Start

The proxy is configured via environment variables. Minimal required env:

- `MATRIX_HOMESERVER_URL`: URL of your Matrix homeserver (e.g. `https://matrix.example`),
  used also to derive the hostname when constructing Matrix IDs from external auth responses
- `SUPER_ADMIN_TOKEN`: the Application Service `as_token` from your registration file
- `PROXY_PORT` (optional): port to listen on (default: `8080`)
- `AS_USER_ID` (optional): the user ID of the Application Service bot (default: `@_acrobits_proxy:matrix.example`)
- `PROXY_URL` (optional): public-facing URL of this proxy (e.g. `https://matrix.example.com`), if not specified, use the value of `MATRIX_HOMESERVER_URL`
 - `EXT_AUTH_URL` (optional): external HTTP endpoint used to validate extension+password for push token reports (default: `https://voice.gs.nethserver.net/freepbx/testextauth`)
 - `EXT_AUTH_TIMEOUT_S` (optional): timeout in seconds for calls to `EXT_AUTH_URL` (default: `5`)
- `LOGLEVEL` (optional): logging verbosity level - `DEBUG`, `INFO`, `WARNING`, `CRITICAL` (default: `INFO`)
- `PUSH_TOKEN_DB_PATH` (optional): path to a database file for storing push tokens
- `CACHE_TTL_SECONDS` (optional): time-to-live for in-memory cache entries (default: `3600` seconds)

### Start with Podman

Run the following command to start the container using rootless Podman:
```
podman run --rm --replace --name matrix2acrobits --network host -e LOGLEVEL=debug  -e MATRIX_HOMESERVER_URL=https://synapse.gs.nethserver.net -e SUPER_ADMIN_TOKEN=secret -e PROXY_PORT=8080 -e AS_USER_ID=@_acrobits_proxy:synapse.gs.nethserver.net -e PROXY_URL=https://synapse.gs.nethserver.net/ -e EXT_AUTH_URL=https://voice.gs.nethserver.net/freepbx/rest/testextauth ghcr.io/nethesis/matrix2acrobits
```

On production set also:

- `PUSH_TOKEN_DB_PATH` to a persistent it inside a volume
- `LOGLEVEL` to `INFO` or `WARNING`

## Building

Build is automated via GitHub Actions and container images are published to GitHub Container Registry: `ghcr.io/nethesis/matrix2acrobits`.

Build golang binary locally:
```bash
# build (produces ./matrix2acrobits)
go build -o matrix2acrobits .
```

Build container image locally:
```bash
buildah build --layers -t ghcr.io/nethesis/matrix2acrobits:latest -f Containerfile .
```

## Extra info

- [Deploying with NS8](docs/DEPLOY_NS8.md)
- [OpenAPI Specification](docs/openapi.yaml)
- [Container Build & Usage](docs/CONTAINER.md)
- [Direct messaging](docs/DIRECT_ROOMS-ALIASES.md)
- [Push Notifications](docs/PUSH_NOTIFICATIONS.md)
- [Authentication](docs/AUTHENTICATION.md)
- [Testing](test/README.md)


## Acrobits documentation

Implemented APIs:

- https://doc.acrobits.net/api/client/fetch_messages_modern.html
- https://doc.acrobits.net/api/client/send_message.html
- https://doc.acrobits.net/api/client/push_token_reporter.html

## Limitations and Future Work

Limitations:

- when a private room is deleted, there is no way to send messages to the user
- text-only messages are supported (no media, no rich content)
- only one-to-one direct messaging is supported (no group chats)

The following features are not yet implemented:

- Account removal: https://doc.acrobits.net/api/client/account_removal_reporter.html#account-removal-reporter-webservice
- Messages with media content
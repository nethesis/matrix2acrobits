# Matrix to Acrobits Proxy

This service acts as a proxy between an Acrobits softphone client and a Matrix homeserver, allowing users to send and receive Matrix messages through an SMS-like interface.

## Technical Overview

The proxy is written in Go and uses the following key technologies:
- **Web Framework**: `github.com/labstack/echo/v4`
- **Matrix Client Library**: `maunium.net/go/mautrix`

The service authenticates to the Matrix homeserver as an **Application Service**, which grants it the ability to perform actions (like sending messages) on behalf of other Matrix users (impersonation).

## Synapse Configuration

To function correctly, the proxy must be registered as an Application Service with your Synapse homeserver.

### 1. Registration File

First, create a registration YAML file (e.g., `acrobits-proxy.yaml`) and place it on your homeserver. This file tells Synapse how to communicate with the proxy.

**`acrobits-proxy.yaml`:**
```yaml
# A unique identifier for the application service.
id: acrobits-proxy
# The URL where Synapse can reach your proxy.
# This may not be used for sending messages but is a required field.
url: http://localhost:8080 
# A secure, randomly generated token your proxy will use to authenticate with Synapse.
as_token: "YOUR_SECURE_APPLICATION_SERVICE_TOKEN"
# A secure, randomly generated token Synapse will use to authenticate with your proxy.
hs_token: "YOUR_SECURE_HOMESERVER_TOKEN"
# The localpart of the 'bot' user for this application service.
sender_localpart: _acrobits_proxy
# This section grants the proxy the power to impersonate users.
namespaces:
  users:
    - exclusive: true
      # This regex must match the user IDs the proxy should control.
      # Setting exclusive: true means the AppService can auto-provision users
      # matching this regex if they don't already exist.
      # Replace with your actual homeserver name.
      regex: '@.*:your-homeserver-name.com'
  aliases: []
  rooms: []
```
*You must generate your own secure random strings for `as_token` and `hs_token`.*

### 2. homeserver.yaml

Next, add the path to your registration file to your Synapse `homeserver.yaml`:

```yaml
app_service_config_files:
  - "/path/to/your/acrobits-proxy.yaml"
```

Finally, **restart your Synapse server** to load the new configuration.

## Proxy Configuration & Running

The proxy is configured via environment variables.

### Environment Variables

- `PROXY_PORT`: The port for the proxy to listen on (default: `8080`).
- `MATRIX_HOMESERVER_URL`: The full URL of your Matrix homeserver (e.g., `https://matrix.your-homeserver-name.com`).
- `SUPER_ADMIN_TOKEN`: The Application Service token (`as_token`) you defined in the registration file.

### Building and Running

1.  **Build the binary:**
    ```shell
    go build -o matrix2acrobits ./cmd/server
    ```
2.  **Run the server:**
    ```shell
    export PROXY_PORT=8080
    export MATRIX_HOMESERVER_URL="https://matrix.your-homeserver-name.com"
    export SUPER_ADMIN_TOKEN="YOUR_SECURE_APPLICATION_SERVICE_TOKEN"
    
    ./matrix2acrobits
    ```

## API Endpoints

### Client API

These endpoints are used by the Acrobits client. The `password` fields in the requests are ignored, as authentication is handled by the Application Service.

- `POST /api/client/send_message`: Sends a message to a Matrix room on behalf of a user. The `from` field in the JSON body specifies the Matrix user to impersonate.
- `POST /api/client/fetch_messages`: Fetches new messages for a user by performing a Matrix sync. The `username` field specifies the Matrix user to impersonate.

### Internal API

These endpoints are for managing the service and are protected. Access requires passing the `SUPER_ADMIN_TOKEN` in the `X-Super-Admin-Token` HTTP header.

- `POST /api/internal/map_sms_to_matrix`: Creates a mapping between a phone number and a Matrix room ID.
- `GET /api/internal/map_sms_to_matrix`: Looks up a mapping.

## Running Integration Tests

Integration tests verify the end-to-end functionality of the proxy by interacting with a live Matrix homeserver.

**Prerequisites:**

- A running Synapse homeserver configured as described in the "Synapse Configuration" section, with the Application Service correctly loaded and permissions granted.
- The `test.env` file (located in the project root) populated with valid Matrix user credentials and homeserver details for the test users.

**How to Run:**

1.  **Set Environment Variables:** Ensure `MATRIX_HOMESERVER_URL` and `SUPER_ADMIN_TOKEN` are set (as described in "Proxy Configuration"). Additionally, set `AS_USER_ID` and `RUN_INTEGRATION_TESTS` as follows:
    ```shell
    export AS_USER_ID="@_acrobits_proxy:your-homeserver-name.com" # Replace with your actual AS user ID
    export RUN_INTEGRATION_TESTS=1
    ```
2.  **Execute Tests:**
    ```shell
    go test -v ./...
    ```

**Note:** These tests are dependent on a live external service and may be flaky if the network is unstable or the server is misconfigured. Due to the external nature of these tests, potential failures might indicate issues with the Synapse server setup rather than the proxy code.


## Acrobits documentation

- https://doc.acrobits.net/api/client/fetch_messages_modern.html
- https://doc.acrobits.net/api/client/send_message.html

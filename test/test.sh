#!/usr/bin/env bash
set -euo pipefail

# test.sh - start/stop/run simplified test environment (Synapse only) and create users
#
# Actions:
#  start : Start a local Synapse container and create test users (if `test/test.env` exists)
#  run   : Run tests — runs unit tests with coverage, then runs integration tests.
#          Requires `test/test.env` in the `test/` directory and tools: `podman`, `jq`, `curl`, `go`.
#  stop  : Stop and remove the Synapse container
#
# Usage: ./test.sh start|run|stop

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$#" -lt 1 ]; then
  echo "Usage: $0 start|run|stop"
  exit 1
fi

action="$1"

stop() {
  echo "Stopping test stack..."
  if podman ps -a --format json | jq -e '.[] | select(.Names[]?=="matrix-synapse")' >/dev/null 2>&1; then
    podman rm -f matrix-synapse || true
    echo "matrix-synapse removed"
  else
    echo "matrix-synapse not present"
  fi

  if [ -n "${MOCK_AUTH_PID:-}" ]; then
    kill "$MOCK_AUTH_PID" 2>/dev/null || true
    echo "Mock auth server stopped"
  fi
}

start() {
  # Check podman available
  if ! command -v podman >/dev/null 2>&1; then
    echo "podman not found in PATH"
    exit 1
  fi

  # Remove any existing container with the same name
  if podman ps -a --format json | jq -e '.[] | select(.Names[]?=="matrix-synapse")' >/dev/null 2>&1; then
    echo "Removing existing container 'matrix-synapse'"
    podman rm -f matrix-synapse >/dev/null 2>&1 || true
  fi

  echo "Starting Synapse container using podman run"
  podman run --replace --rm -d --name matrix-synapse \
    -p 8008:8008 \
    -v "$SCRIPT_DIR/homeserver.yaml":/data/homeserver.yaml:ro,Z \
    -v "$SCRIPT_DIR/acrobits-proxy.yaml":/data/acrobits-proxy.yaml:ro,Z \
    -v "$SCRIPT_DIR/log.config":/data/config/log.config:ro,Z \
    docker.io/matrixdotorg/synapse:latest >/dev/null

  # Wait for synapse to be healthy
  echo "Waiting for matrix-synapse to be healthy (up to 120s)"
  for i in {1..24}; do
    if podman ps --format json | jq -e ".[] | select(.Names[]? == \"matrix-synapse\") | select(.State == \"running\")" >/dev/null 2>&1; then
      if curl -sf http://localhost:8008/_matrix/client/versions >/dev/null 2>&1; then
        echo "matrix-synapse is healthy."
        break
      fi
    fi
    sleep 5
  done

  echo "Starting mock auth server..."
  pushd "$SCRIPT_DIR"
  go run mock_auth.go &
  MOCK_AUTH_PID=$!
  popd
  echo "Mock auth server started with PID $MOCK_AUTH_PID"
  # Wait for mock auth
  sleep 2

  # Source test environment variables
  if [ -f "$SCRIPT_DIR/test.env" ]; then
    set -a
    source "$SCRIPT_DIR/test.env"
    set +a
  else
    echo "Warning: test.env not found. Skipping user creation."
    return
  fi

  # Function to create Matrix user via admin API
  create_matrix_user() {
    local username="$1"
    local password="$2"

    echo "Creating Matrix user: ${username}"
    local response
    response=$(curl -s -w "\n%{http_code}" -X PUT "http://localhost:8008/_synapse/admin/v2/users/@${username}" \
      -H "Authorization: Bearer ${MATRIX_AS_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "{\"password\": \"${password}\", \"admin\": false}" 2>&1)

    local http_code=$(echo "$response" | tail -n1)
    if [[ "$http_code" =~ ^(200|201)$ ]]; then
      echo "Successfully created Matrix user ${username}"
    else
      echo "Note: Matrix user ${username} - API returned HTTP ${http_code} (may authenticate via OIDC)"
    fi
  }

  echo "Creating Matrix test users..."
  if [ -n "${USER1:-}" ] && [ -n "${USER1_PASSWORD:-}" ]; then
    # Prefer container-side registration helper if available
    if podman exec matrix-synapse which register_new_matrix_user >/dev/null 2>&1; then
      echo "Registering ${USER1} inside container using shared secret"
      podman exec matrix-synapse /usr/local/bin/register_new_matrix_user -u "$(echo "${USER1}" | cut -d'@' -f1)" -p "${USER1_PASSWORD}" -k "${REGISTRATION_SHARED_SECRET}" --no-admin || true
    else
      create_matrix_user "${USER1}" "${USER1_PASSWORD}"
    fi
  fi

  if [ -n "${USER2:-}" ] && [ -n "${USER2_PASSWORD:-}" ]; then
    if podman exec matrix-synapse which register_new_matrix_user >/dev/null 2>&1; then
      echo "Registering ${USER2} inside container using shared secret"
      podman exec matrix-synapse /usr/local/bin/register_new_matrix_user -u "$(echo "${USER2}" | cut -d'@' -f1)" -p "${USER2_PASSWORD}" -k "${REGISTRATION_SHARED_SECRET}" --no-admin || true
    else
      create_matrix_user "${USER2}" "${USER2_PASSWORD}"
    fi
  fi

  echo "Test environment ready!"
  echo "Synapse: http://localhost:8008"

  # --- Verification: send a message from USER1 to USER2 and verify receipt ---
  if command -v jq >/dev/null 2>&1; then
    HS="http://localhost:8008"
    SERVER_NAME=$(awk '/^server_name:/ {print $2; exit}' "$SCRIPT_DIR/homeserver.yaml" || echo "localhost")
    user1_local=$(echo "${USER1}" | cut -d'@' -f1)
    user2_local=$(echo "${USER2}" | cut -d'@' -f1)
    user1_mxid="@${user1_local}:${SERVER_NAME}"
    user2_mxid="@${user2_local}:${SERVER_NAME}"

    echo "Verifying messaging: ${user1_mxid} -> ${user2_mxid}"

    login_user() {
      local full="$1" pw="$2"
      local localpart=$(echo "$full" | cut -d'@' -f1)
      local resp
      resp=$(curl -s -X POST "$HS/_matrix/client/v3/login" -H "Content-Type: application/json" \
        -d "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"${localpart}\"},\"password\":\"${pw}\"}")
      echo "$resp" | jq -r '.access_token // empty'
    }

    token1=$(login_user "${USER1}" "${USER1_PASSWORD}") || true
    token2=$(login_user "${USER2}" "${USER2_PASSWORD}") || true

    if [ -z "${token1}" ] || [ -z "${token2}" ]; then
      echo "Warning: could not obtain tokens for users; skipping message verification."
      return
    fi

    # Create a room as USER1 and invite USER2
    create_room_resp=$(curl -s -X POST "$HS/_matrix/client/v3/createRoom" \
      -H "Authorization: Bearer ${token1}" -H "Content-Type: application/json" \
      -d "{\"invite\":[\"${user2_mxid}\"],\"is_direct\":true}")
    room_id=$(echo "$create_room_resp" | jq -r '.room_id // empty')
    if [ -z "$room_id" ]; then
      echo "Warning: failed to create room for verification. Response: $create_room_resp"
      return
    fi
    echo "Created room: $room_id"

    # Send a message as USER1
    txnid=$(date +%s%N)
    send_resp=$(curl -s -X PUT "$HS/_matrix/client/v3/rooms/${room_id}/send/m.room.message/${txnid}" \
      -H "Authorization: Bearer ${token1}" -H "Content-Type: application/json" \
      -d '{"msgtype":"m.text","body":"Test message from USER1"}')

    # Have USER2 join the room (ensure membership)
    join_resp=$(curl -s -X POST "$HS/_matrix/client/v3/rooms/${room_id}/join" \
      -H "Authorization: Bearer ${token2}" -H "Content-Type: application/json")

    # Fetch recent messages as USER2
    sleep 1
    messages_resp=$(curl -s -X GET "$HS/_matrix/client/v3/rooms/${room_id}/messages?dir=b&limit=20" \
      -H "Authorization: Bearer ${token2}")

    if echo "$messages_resp" | jq -e '.chunk[] | select(.type=="m.room.message" and (.content.body|test("Test message from USER1")))' >/dev/null 2>&1; then
      echo "Message verification succeeded: USER2 received the message."
    else
      echo "Warning: message not found in room messages. Response summary:"
      echo "$messages_resp" | jq -c '.chunk[] | {type: .type, body: .content.body} ' | sed -n '1,10p'
    fi
  else
    echo "jq not found; skipping message verification step."
  fi
}

run() {
  # Source test environment variables
  if [ -f "$SCRIPT_DIR/test.env" ]; then
    set -a
    source "$SCRIPT_DIR/test.env"
    set +a
  else
    echo "test.env not found; cannot run integration tests"
    exit 1
  fi

  # If podman is available, ensure the synapse container is running; start it otherwise.
  if command -v podman >/dev/null 2>&1; then
    if ! podman ps --format json | jq -e ".[] | select(.Names[]? == \"matrix-synapse\" and .State == \"running\")" >/dev/null 2>&1; then
      echo "matrix-synapse not running; starting..."
      start
      echo "Waiting a few seconds for synapse to initialize..."
      sleep 5
    else
      echo "matrix-synapse already running"
    fi
  else
    echo "podman not found in PATH; please start Synapse manually if you want integration tests to run against a local server"
  fi

  export EXT_AUTH_URL=http://localhost:18081
  export RUN_INTEGRATION_TESTS=1
  echo "Running integration tests (RUN_INTEGRATION_TESTS=1)"
  # Run all tests with coverage first
  echo "Running unit tests and collecting coverage..."
  pushd ..
  go test -v ./... -coverprofile=coverage.out
  popd
}

case "$action" in
  start)
    start
    ;;
  run)
    run
    ;;
  stop)
    stop
    ;;
  *)
    echo "Unknown action: $action"
    echo "Usage: $0 start|run|stop"
    exit 1
    ;;
esac

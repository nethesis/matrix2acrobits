# Deploy

The deploy consists of two main steps:
1. Configure Acrobits provisioning
2. Configure Synapse to register the Application Service

## Acrobits

Substutute `synapse.gs.nethserver.net` with your Matrix homeserver name.

- **General Messaging Configuration**: select on **Web Service** datasource
- **Outgoing SMS via Web Service**:
  - URL (first field): `https://synapse.gs.nethserver.net/m2a/api/client/send_message`
  - POST data (second field): `{ "from" : "%account[username]%", "password" : "%account[password]%", "to" : "%sms_to%", "body" : "%sms_body%" }`
  - Content-Type (third field): `application/json`
- **Fetch instant messages via Web Service**:
  - URL (first field): `https://synapse.gs.nethserver.net/m2a/api/client/fetch_messages`
  - POST data (second field): `{ "username" : "%account[username]%", "password" : "%account[password]%",   "last_id" : "%last_known_sms_id%", "last_sent_id" : "%last_known_sent_sms_id%", "device" : "%installid%" }`
  - Content-Type (third field): `application/json`
- **Push Token Reporter Web Service**:
  - URL (first field): `https://synapse.gs.nethserver.net/m2a/api/client/push_token_report`
  - POST data (second field): `{ "username" : "%account[username]%", "password" : "%account[password]%", "token_calls" : "%pushTokenIncomingCall%", "token_msgs" : "%pushTokenOther%", "selector" : "%selector%", "appId_calls": "%pushappid_incoming_call%", "appId_msgs" : "%pushappid_other%" }`
  - Content-Type (third field): `application/json`

## NethServer 8

To function correctly, the proxy must be registered as an Application Service with your Synapse homeserver.

## Manual setup on an existing NS8 installation

Install matrix:
```
add-module ghcr.io/nethserver/matrix:latest
```

Enter the matrix user shell:
```
runagent -m matrix1
```

Download the container image:
```
podman pull ghcr.io/nethesis/matrix2acrobits:latest
```

First, create a registration YAML file (e.g., `acrobits-proxy.yaml`) and place it on your homeserver. This file tells Synapse how to communicate with the proxy.
```
cat <<EOF >synapse-config/acrobits-proxy.yaml
id: acrobits-proxy
url: http://localhost:8080 
as_token: "secret"
hs_token: "secret"
sender_localpart: _acrobits_proxy
namespaces:
  users:
    - exclusive: false
      regex: '@.*'
  aliases:
    - exclusive: false
      regex: '.*'
  rooms:
    - exclusive: false
      regex: '.*'
EOF
```

Next, add the path to your registration file to your Synapse `homeserver.yaml`:

```
echo "app_service_config_files:" >> ../templates/synapse-homeserver.yaml
echo "   - /data/config/acrobits-proxy.yaml" >> ../templates/synapse-homeserver.yaml
```

You must generate your own secure random strings for `as_token` and `hs_token`.

Restart the service:
```
systemctl --user restart synapse
```

Start the matrix2acrobits container:
```
podman run -d --rm --replace --name matrix2acrobits --network host -e LOGLEVEL=debug  -e MATRIX_HOMESERVER_URL=https://synapse.gs.nethserver.net -e MATRIX_AS_TOKEN=secret -e PROXY_PORT=8080 -e AS_USER_ID=@_acrobits_proxy:synapse.gs.nethserver.net -e PROXY_URL=https://synapse.gs.nethserver.net/ -e EXT_AUTH_URL=https://voice.gs.nethserver.net/freepbx/rest/testextauth ghcr.io/nethesis/matrix2acrobits
```

Configure traefik to route /m2a to the proxy:
```
api-cli run set-route  --agent module/traefik1 --data '{"instance": "matrix1-m2a", "name":"synapse-m2a","host":"synapse.gs.nethserver.net","path":"/m2a","url":"http://localhost:8080","lets_encrypt":true, "strip_prefix": true}'
```

Configure traefik to route /_matrix/push/v1/notify to the proxy:
```
api-cli run set-route  --agent module/traefik1 --data '{"instance": "matrix1-push", "name":"synapse-push","host":"synapse.gs.nethserver.net","path":"/_matrix/push/v1/notify","url":"http://localhost:8080","lets_encrypt":true, "strip_prefix": false}'
```

Now:
- login to Element with the first user (giacomo)
- login to Element with the second user (mario)

After the above logins, you can send a message from giacomo (91201) to mario (202) using the proxy API:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "91201",
    "password": "giacomo",
    "to": "202",
    "body": "Hello Mario — this is Giacomo (curl test)",
    "content_type": "text/plain"
  }'
```

Send message using Matrix ID as recipient - Giacomo to Mario:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "91201",
    "password": "giacomo",
    "to": "@mario:synapse.gs.nethserver.net",
    "body": "Hello Mario — this is Giacomo (curl test)",
    "content_type": "text/plain"
  }'
```


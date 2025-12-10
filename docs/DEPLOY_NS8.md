# Deploy NS8

To function correctly, the proxy must be registered as an Application Service with your Synapse homeserver.

## General instructions

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
    - exclusive: false
      regex: '@.*:your-homeserver-name.com'
  aliases: []
  rooms: []
```
*You must generate your own secure random strings for `as_token` and `hs_token`.*

### 2. homeserver.yaml

Next, add the path to your registration file to your Synapse `homeserver.yaml`:

```yaml
app_service_config_files:
  - "/data/config/acrobits-proxy.yaml"
```

Finally, **restart your Synapse server** to load the new configuration.


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


Setup synapse application service:
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

echo "app_service_config_files:" >> ../templates/synapse-homeserver.yaml
echo "   - /data/config/acrobits-proxy.yaml" >> ../templates/synapse-homeserver.yaml
```

Restart the services:
```
systemctl --user restart synapse
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


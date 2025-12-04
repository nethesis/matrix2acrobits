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

### 3. dex_config.yaml

Add this do the end of the config file:
```yaml
oauth2:
  skipApprovalScreen: true
  passwordConnector: ldap
```

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
podman pull ghcr.io/nethesis/matrix2acrobits
```

Setup dex to use ldap password connector:
```
echo >> ../templates/dex-config.yaml
echo '  passwordConnector: ldap' >> ../templates/dex-config.yaml
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
      regex: '@.*:.*'
  aliases: []
  rooms: []
EOF

echo "app_service_config_files:" >> ../templates/synapse-homeserver.yaml
echo "   - /data/config/acrobits-proxy.yaml" >> ../templates/synapse-homeserver.yaml
```

Restart the services:
```
systemctl --user restart synapse
systemctl --user restart dex
```

Run the container:
```
podman run --rm --replace --name matrix2acrobits --network host -e MATRIX_HOMESERVER_URL=https://synapse.gs.nethserver.net -e SUPER_ADMIN_TOKEN=secret -e PROXY_PORT=8080 -e AS_USER_ID=@_acrobits_proxy:synapse.gs.nethserver.net ghcr.io/nethesis/matrix2acrobits
```

Configure traefik to route /m2a to the proxy:
```
api-cli run set-route  --agent module/traefik1 --data '{"instance": "matrix1-m2a", "name":"synapse-m2a","host":"synapse.gs.nethserver.net","path":"/m2a","url":"http://localhost:8080","lets_encrypt":true, "strip_prefix": true}'
```

Now:
- login to Element with the first user (giacomo)
- login to Element with the second user (mario)

After the above logins, you can send a message from giacomo to mario:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "@giacomo:synapse.gs.nethserver.net",
    "sms_to": "@mario:synapse.gs.nethserver.net",
    "sms_body": "Hello Mario — this is Giacomo (curl test)",
    "content_type": "text/plain"
  }'
```

Response example:
```
{"sms_id":"$VnrNZPmkkrgcqd2Lq15K9GKYuKXaNi-PrEsx6WLHfDs"}
```

Mario reply:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "@mario:synapse.gs.nethserver.net",
    "sms_to": "@giacomo:synapse.gs.nethserver.net",
    "sms_body": "Hello Giacomo — this is Mario reply (curl test)",
    "content_type": "text/plain"
  }'
```


Map SMS number (201) to Matrix user (giacomo):
```
curl -v -X POST "http://127.0.0.1:8080/api/internal/map_sms_to_matrix"   -H "Content-Type: application/json"   -H "X-Super-Admin-Token: secret"   -d '{
  "sms_number": "201",
  "matrix_id": "@giacomo:synapse.gs.nethserver.net",
  "room_id": "!giacomo-room:synapse.gs.nethserver.net"
}'
```

Map SMS number (202) to Matrix user (mario):
```
curl -v -X POST "http://127.0.0.1:8080/api/internal/map_sms_to_matrix"   -H "Content-Type: application/json"   -H "X-Super-Admin-Token: secret"   -d '{
  "sms_number": "202",
  "matrix_id": "@mario:synapse.gs.nethserver.net",
  "room_id": "!mario-room:synapse.gs.nethserver.net"
}'
```

Retrieve current mappings:
```
curl "http://127.0.0.1:8080/api/internal/map_sms_to_matrix" -H "X-Super-Admin-Token: secret"
```

Send message using mapped SMS number (201) - Giacomo to Mario:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "@giacomo:synapse.gs.nethserver.net",
    "sms_to": "202",
    "sms_body": "Hello Mario — this is Giacomo (curl test using mapped number)",
    "content_type": "text/plain"
  }'
```

Send message using mapped SMS number (202) - Mario to Giacomo:
```
curl -s -X POST   https://synapse.gs.nethserver.net/m2a/api/client/send_message   -H "Content-Type: application/json"   -d '{
    "from": "@mario:synapse.gs.nethserver.net",
    "sms_to": "201",
    "sms_body": "Hello Giacomo — this is Mario reply (curl test using mapped number)",
    "content_type": "text/plain"
  }'
```
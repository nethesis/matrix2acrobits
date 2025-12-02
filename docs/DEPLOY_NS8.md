# Deploy NS8

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
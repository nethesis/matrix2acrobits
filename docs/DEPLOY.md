# Deploy

The deploy consists of two main steps:
1. Configure Acrobits provisioning
2. Configure Synapse to register the Application Service

## Acrobits

Replace `synapse.gs.nethserver.net` with your Matrix homeserver name.

The following features must be enabled:
- *Incoming and Outgoing Messages via Web Service*
- *Rich Messaging*, needed only to allow to send attachments (not implemented yet)

Automatic provisioning:

- deploy `ctiapp-authproxy` from the [this PR](https://github.com/nethesis/ctiapp-authproxy/pull/16)
- set the deployed hostname inside the **External provisioning** field

As an alternative to automatic provisioning, manual provisioning options:

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

The following configurations are required:
- a create a registration YAML file (e.g., `acrobits-proxy.yaml`): this file tells Synapse how to communicate with the proxy
- an updated `homeserver.yaml` to include the Application Service registration file
- a route to `/m2a` in traefik to point to the proxy
- a route to `/_matrix/push/v1/notify` in traefik to point to the proxy (for push notifications)

Everything is already implemented inside [ns8-matrix](https://github.com/NethServer/ns8-matrix) module.

### Matrix <-> Acrobits Mobile App Integration Testing

This is the current procedure to test the integration.

### Users

1. Install a local LDAP domain.
2. Create at least two users (`user1` and `user2`).

### Matrix

1. Install Matrix on NethServer 8: `add-module ghcr.io/nethserver/matrix:latest` (will be available in the forge soon).
2. Configure Matrix using the UI; leave the NethVoice-related field empty (we will configure it later).
3. Enable at least one client (I recommend Cinny as it is simpler).
4. Log in with `user1` and `user2` on the Matrix client.

### NethVoice

1. Install the experimental version of NethVoice: `add-module ghcr.io/nethesis/nethvoice:matrix_integration`.
2. Configure NethVoice by associating it with the LDAP domain created above.
3. Configure the two users in the wizard, ensuring you assign the mobile app extension to both.
4. Configure the Matrix integration:
Retrieve the Matrix UUID: `redis-cli hget module/matrix1/environment MODULE_UUID`, and use it in the command below:
```bash
api-cli run module/nethvoice1/set-matrix-server --data '{"module_uuid": "cf50b191-95d5-435b-bf34-0905bf7dba55"}'
```

### Mobile App

To test the app, you must use the [NETHTEST](https://providers.cloudsoftphone.com/record/detail/15482) version.

1. Download the [Cloud Softphone](https://play.google.com/store/apps/details?id=cz.acrobits.softphone.cloudphone&hl=it) application.
2. Log in with these credentials:
  * **Username:** `user1@<fqdn_nethvoice>@NETHTEST*`
  * **Password:** `<password_user1>`
  (If this login does not work, you need to create a dummy QR code on the NETHTEST app).
3. Try sending a message to another user: the message should be delivered to the Matrix client, and replies should return to the app.
4. Write a message from Cinny to the app (a room will open after step 3); the message should be delivered via push notification if the app is closed.


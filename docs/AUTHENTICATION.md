# Authentication

All client API endpoints (`/api/client/fetch_messages`, `/api/client/send_message`, `/api/client/push_token_report`) require authentication via an external authentication service.

The external authentication service is implemented inside NethVoice FreePBX container [REST APIs](https://github.com/nethesis/ns8-nethvoice/tree/main/freepbx/var/www/html/freepbx/rest).

### External Auth Flow

When a client sends a request, the proxy:
1. Extracts the `username` (extension) and `password` from the request.
2. Calls `EXT_AUTH_URL` with a POST request containing JSON: `{"extension":"<username>","secret":"<password>"}`.
3. On successful auth (200), parses the response for `main_extension`, `sub_extensions`, and `user_name`, which are converted into a mapping and saved.
4. On failure (401 or other error), returns an authentication error and does NOT save the push token or create a mapping.
5. Auth responses are cached in-memory for `CACHE_TTL_SECONDS` seconds to reduce external service load.

If any request is missing a `password`, it fails with authentication error.

### Environment variables related to auth

- `EXT_AUTH_URL`: external HTTP endpoint used to validate extension+password for push token reports (eg: `https://voice.nethserver.org/freepbx/rest/testextauth`)
- `EXT_AUTH_TIMEOUT_S`: timeout in seconds for calls to `EXT_AUTH_URL` (default: `5`)
- `CACHE_TTL_SECONDS`: cache TTL for external auth responses (default: `3600` seconds)

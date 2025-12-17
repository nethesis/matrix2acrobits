# Authentication

All client API endpoints (`/api/client/fetch_messages`, `/api/client/send_message`, `/api/client/push_token_report`) require authentication via an external authentication service.

The external authentication service is provided by NethCTI Middleware, which manages user credentials, chat capabilities, and Matrix homeserver configuration.

### External Auth Flow (2-Step Process)

When a client sends a request with `username` and `password`, the proxy performs a 2-step authentication:

**Step 1: Login (POST to `/api/login`)**
1. Extract the username part from the `username` field (e.g., if `username` is `user@domain.com`, extract `user`)
2. POST to `{EXT_AUTH_URL}/api/login` with JSON payload: `{"username":"<user>","password":"<password>"}`
3. On successful auth (200), parse the response to get the JWT `token` field
4. Decode the JWT to extract claims (without signature verification)

**Step 2: Verify Chat Capability & Fetch Configuration (GET from `/api/chat?users=1`)**
5. Verify that the JWT contains the `nethvoice_cti.chat` claim set to `true`
6. If the claim is missing or false, authentication fails
7. GET from `{EXT_AUTH_URL}/api/chat?users=1` with Bearer token in Authorization header
8. Parse the response to extract:
   - Matrix homeserver configuration (`matrix.base_url`, `matrix.acrobits_url`)
   - User mappings from the `users` array (`user_name`, `main_extension`, `sub_extensions`)
9. Convert user data into `MappingRequest` objects and cache them

**Error Handling**
- On any failure (login error, missing claim, invalid JWT, chat endpoint error), returns authentication error
- Does NOT save the push token or create a mapping on failure
- Successful authentications are cached in-memory for `CACHE_TTL_SECONDS` seconds to reduce external service load

If any request is missing a `password`, it fails with authentication error.

### Environment variables related to auth

- `EXT_AUTH_URL`: Base URL of the external authentication service (eg: `https://cti.nethserver.org/`). The proxy automatically appends `/api/login` and `/api/chat?users=1` to this URL.
- `EXT_AUTH_TIMEOUT_S`: timeout in seconds for calls to `EXT_AUTH_URL` (default: `5`)
- `CACHE_TTL_SECONDS`: cache TTL for external auth responses (default: `3600` seconds)

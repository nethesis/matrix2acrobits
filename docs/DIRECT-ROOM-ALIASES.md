# Direct Room Aliases (brief)

This document explains how direct 1:1 room mapping works in `service/messages.go` using deterministic room aliases.

## Summary

- The service uses a deterministic alias key of the form `localpartA|localpartB` to find or create a 1:1 Matrix room.
- `generateRoomAliasKey` normalizes both Matrix user IDs (strips leading `@`, drops domain part, lowercases) and orders them lexicographically so the alias is the same from both sides.
- `ensureDirectRoom` first checks a local cache (`roomAliasCache`), then asks the Matrix server (`ResolveRoomAlias`), and finally creates the room (`CreateDirectRoom`) if missing.
- After creating a room the service ensures the target user joins the room so it appears in their `/sync` results.

## Alias key generation

- Given two users: `@alice:example.org` and `@bob:example.org`.
- Normalized localparts are `alice` and `bob`.
- Ordered lexicographically the alias key becomes `alice|bob`.
- The room alias used by the service is this same string (the Matrix client wrapper adds the `#` and domain when interacting with the server).

## Resolution flow

1. Caller requests to send a message from `From` to `To`.
2. Both identifiers are resolved to Matrix user IDs via mappings or direct `@user:domain` input.
3. `ensureDirectRoom` computes the alias key using `generateRoomAliasKey`.
4. Check `roomAliasCache` for a cached room ID.
5. If missing, call `matrixClient.ResolveRoomAlias(ctx, key)` to see if the room already exists on the homeserver.
6. If still missing, call `matrixClient.CreateDirectRoom(ctx, actingUserID, targetUserID, key)` to create the room and cache the result.
7. Ensure the target user joins the room so they receive it in subsequent `/sync` results.

Notes about participant resolution

- When presenting the "other" participant during `/sync` processing the service prefers deriving the other user's identifier from room aliases (it strips `#`/domain, splits by `|`, matches the localpart that isn't `me`, then looks up mappings to return the configured phone `Number` if available).
- Resolved identifiers are cached in `roomParticipantCache` to avoid repeated matrix queries.

Short examples

- Create/find direct room for `@_acrobits_proxy:example.org` and `@alice:example.org`:

  - Normalized localparts: `_acrobits_proxy`, `alice` → alias key `_acrobits_proxy|alice`
  - If no room exists, service calls `CreateDirectRoom(..., "_acrobits_proxy|alice")` and caches the room ID.

- Mapping lookup when formatting messages:

  - If alias localpart `bob` is found and mappings contain `{"number":201, "matrix_id":"@bob:example.org"}`, the service returns `201` as the identifier for the other participant.


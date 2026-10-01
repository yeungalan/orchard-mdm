# REST API reference

Every console action is available over the same JSON API. Generated from `internal/api/api.go`.

**Authentication.** Browser sessions use the `orchard_session` cookie plus an `X-CSRF-Token` header on non-GET requests (the token is returned by `/api/auth/login` and `/api/auth/me`). Scripts use an API key created under Settings → API keys:

```sh
curl -H 'Authorization: Bearer orch_xxxxxxxx_…' https://mdm.example.com/api/devices?compliance=noncompliant
```

**Roles.** Each endpoint needs a minimum role: `read` (any signed-in user), `helpdesk` (device actions), `operator` (configuration), `admin` (users, keys, certificates, settings). Errors are returned as `{"error": "…"}` with a matching HTTP status.

## Authentication

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/auth/status` | public |
| `POST` | `/api/auth/login` | public |
| `POST` | `/api/auth/setup` | public |
| `POST` | `/api/auth/logout` | read |
| `GET` | `/api/auth/me` | read |
| `POST` | `/api/auth/password` | read |

## Dashboard & system

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/dashboard` | read |
| `GET` | `/api/system` | read |
| `GET` | `/api/variables` | read |
| `GET` | `/api/events` | read |
| `GET` | `/api/audit` | admin |

## Devices

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/devices` | read |
| `GET` | `/api/devices/export.csv` | read |
| `GET` | `/api/devices/facets` | read |
| `POST` | `/api/devices/bulk` | helpdesk |
| `GET` | `/api/devices/{udid}` | read |
| `PATCH` | `/api/devices/{udid}` | helpdesk |
| `DELETE` | `/api/devices/{udid}` | operator |
| `GET` | `/api/devices/{udid}/apps` | read |
| `GET` | `/api/devices/{udid}/profiles` | read |
| `GET` | `/api/devices/{udid}/certificates` | read |
| `GET` | `/api/devices/{udid}/commands` | read |
| `GET` | `/api/devices/{udid}/events` | read |
| `GET` | `/api/devices/{udid}/telemetry` | read |
| `GET` | `/api/devices/{udid}/locations` | read |
| `GET` | `/api/devices/{udid}/declarations` | read |
| `GET` | `/api/devices/{udid}/assignments` | read |
| `GET` | `/api/devices/{udid}/compliance` | read |
| `POST` | `/api/devices/{udid}/commands` | helpdesk |
| `POST` | `/api/devices/{udid}/actions/{action}` | helpdesk |
| `GET` | `/api/devices/{udid}/secrets` | operator |

## Commands

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/command-catalog` | read |
| `GET` | `/api/commands` | read |
| `GET` | `/api/commands/{uuid}` | read |
| `DELETE` | `/api/commands/{uuid}` | helpdesk |

## Groups

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/groups` | read |
| `POST` | `/api/groups` | operator |
| `POST` | `/api/groups/preview` | read |
| `GET` | `/api/group-rule-fields` | read |
| `GET` | `/api/groups/{id}` | read |
| `PUT` | `/api/groups/{id}` | operator |
| `DELETE` | `/api/groups/{id}` | operator |
| `GET` | `/api/groups/{id}/members` | read |
| `POST` | `/api/groups/{id}/members` | operator |
| `DELETE` | `/api/groups/{id}/members` | operator |

## Assignments

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/assignments` | read |
| `POST` | `/api/assignments` | operator |
| `DELETE` | `/api/assignments/{id}` | operator |

## Configuration profiles

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/profile-schemas` | read |
| `GET` | `/api/profiles` | read |
| `POST` | `/api/profiles` | operator |
| `POST` | `/api/profiles/upload` | operator |
| `GET` | `/api/profiles/{id}` | read |
| `PUT` | `/api/profiles/{id}` | operator |
| `DELETE` | `/api/profiles/{id}` | operator |
| `GET` | `/api/profiles/{id}/download` | read |
| `GET` | `/api/profiles/{id}/status` | read |
| `POST` | `/api/profiles/{id}/retry` | operator |

## Apps

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/apps` | read |
| `GET` | `/api/apps/search` | read |
| `POST` | `/api/apps` | operator |
| `POST` | `/api/apps/enterprise` | operator |
| `GET` | `/api/apps/{id}` | read |
| `PUT` | `/api/apps/{id}` | operator |
| `DELETE` | `/api/apps/{id}` | operator |
| `GET` | `/api/apps/{id}/status` | read |
| `POST` | `/api/apps/{id}/retry` | operator |
| `POST` | `/api/apps/{id}/install` | helpdesk |

## Declarative management

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/declaration-templates` | read |
| `GET` | `/api/declarations` | read |
| `POST` | `/api/declarations` | operator |
| `GET` | `/api/declarations/{id}` | read |
| `PUT` | `/api/declarations/{id}` | operator |
| `DELETE` | `/api/declarations/{id}` | operator |

## Compliance

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/compliance/policies` | read |
| `POST` | `/api/compliance/policies` | operator |
| `GET` | `/api/compliance/policies/{id}` | read |
| `PUT` | `/api/compliance/policies/{id}` | operator |
| `DELETE` | `/api/compliance/policies/{id}` | operator |
| `GET` | `/api/compliance/summary` | read |
| `POST` | `/api/compliance/evaluate` | operator |

## Enrollment

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/enrollment/info` | read |
| `GET` | `/api/enrollment/tokens` | read |
| `POST` | `/api/enrollment/tokens` | operator |
| `PUT` | `/api/enrollment/tokens/{id}` | operator |
| `DELETE` | `/api/enrollment/tokens/{id}` | operator |
| `GET` | `/api/enrollment/profile` | operator |

## Automated Device Enrollment

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/ade/servers` | read |
| `POST` | `/api/ade/servers` | admin |
| `PUT` | `/api/ade/servers/{id}` | admin |
| `DELETE` | `/api/ade/servers/{id}` | admin |
| `GET` | `/api/ade/servers/{id}/publickey` | admin |
| `POST` | `/api/ade/servers/{id}/token` | admin |
| `POST` | `/api/ade/servers/{id}/sync` | operator |
| `GET` | `/api/ade/devices` | read |
| `GET` | `/api/ade/profiles` | read |
| `POST` | `/api/ade/profiles` | operator |
| `PUT` | `/api/ade/profiles/{id}` | operator |
| `DELETE` | `/api/ade/profiles/{id}` | operator |
| `POST` | `/api/ade/assign` | operator |
| `POST` | `/api/ade/unassign` | operator |

## Apps and Books

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/vpp/tokens` | read |
| `POST` | `/api/vpp/tokens` | admin |
| `DELETE` | `/api/vpp/tokens/{id}` | admin |
| `POST` | `/api/vpp/tokens/{id}/sync` | operator |
| `GET` | `/api/vpp/assets` | read |

## Apple Push (APNs)

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/apns` | read |
| `POST` | `/api/apns/csr` | admin |
| `GET` | `/api/apns/csr` | admin |
| `POST` | `/api/apns/vendor-sign` | admin |
| `POST` | `/api/apns/mdmcert` | admin |
| `POST` | `/api/apns/mdmcert/decrypt` | admin |
| `POST` | `/api/apns/certificate` | admin |
| `POST` | `/api/apns/test` | helpdesk |

## Administration

| Method | Path | Minimum role |
|---|---|---|
| `GET` | `/api/settings` | read |
| `PUT` | `/api/settings` | admin |
| `GET` | `/api/users` | admin |
| `POST` | `/api/users` | admin |
| `PUT` | `/api/users/{id}` | admin |
| `DELETE` | `/api/users/{id}` | admin |
| `GET` | `/api/apikeys` | admin |
| `POST` | `/api/apikeys` | admin |
| `DELETE` | `/api/apikeys/{id}` | admin |
| `GET` | `/api/webhooks` | read |
| `POST` | `/api/webhooks` | admin |
| `PUT` | `/api/webhooks/{id}` | admin |
| `DELETE` | `/api/webhooks/{id}` | admin |
| `POST` | `/api/webhooks/{id}/test` | admin |
| `GET` | `/api/pki/ca` | read |
| `GET` | `/api/pki/issued` | read |

## Useful details

- `GET /api/devices` accepts `q`, `status` (`enrolled`, `pending`, `unenrolled`), `compliance`, `model`, `os`, `ownership`, `supervised` (`1`/`0`), `group`, `sort` (`name`, `os`, `battery`, `last_seen`, …), `dir=desc`, `limit`, `offset`.
- `POST /api/devices/{udid}/commands` takes `{"command": "<catalog id>", "params": {…}}`. The catalog (`GET /api/command-catalog`) lists every command with its parameters. Wipe commands (`EraseDevice`, `DeleteUser`) are refused.
- `POST /api/devices/{udid}/actions/{action}` actions: `sync`, `telemetry`, `push`, `reconcile`, `retry-failed`, `clear-queue`, `locate`, `evaluate-compliance`, `renew-identity`, `unenroll`.
- `POST /api/devices/bulk` takes `udids` plus either `command`/`params`, an `action`, `add_to_group`/`remove_from_group` with `group_id`, or `add_tags` with `tags`.
- `GET /api/devices/{udid}/telemetry?hours=168` returns battery/storage/network samples and the networks seen; `GET /api/devices/{udid}/locations?days=30` returns location fixes.
- Uploads (profiles, certificates, tokens) are sent as base64 in JSON (`{"data": "…"}`); enterprise `.ipa` files use `multipart/form-data` with a `file` field.

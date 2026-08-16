# Dym CLI design

## Purpose

`dym` is a Go command-line client for the existing Dymmer HTTP API. Its
initial release provides safe API-token authentication and useful operations
for DNS records, project secrets, mailboxes, and forwarding rules. The command
language and all runtime messages are English.

The CLI targets `https://dymmer.com/api/v1` by default. It authenticates each
request with `Authorization: Bearer <token>`.

## Command model

Commands put the primary resource immediately after its group. A domain is the
context for all its managed resources, which keeps related operations together:

```text
dym auth login
dym auth logout
dym auth status
dym auth token-help

dym domain <domain> records list [--type A]
dym domain <domain> records create --type A --name www --value 203.0.113.10
dym domain <domain> records update <id> [record flags]
dym domain <domain> records delete <id> [--yes]
dym domain <domain> mailboxes list
dym domain <domain> forwardings list

dym project <project> secrets get [--env dev] [--deployment name] [--format json|dotenv]
```

The first release does not include `srv`. Mailboxes and forwardings are domain
resources from the user's perspective, so they remain below `dym domain`.
Future mutation commands can extend the existing resource groups without
changing this interface.

## Authentication and credential security

`dym auth login` accepts a token through a masked terminal prompt. It stores
the token in the native system credential store (macOS Keychain, Windows
Credential Manager, or Secret Service on Linux) under a dedicated Dymmer CLI
service/account. The configuration file stores no secret; it contains only
non-sensitive settings such as the API base URL.

`DYMMER_TOKEN` is an explicit, higher-priority override for CI and other
non-interactive use. The CLI must never accept a token as a command argument
or flag, and never include it in output, errors, logs, or diagnostics.

If the system credential store is unavailable, `auth login` fails with an
actionable error rather than silently persisting a plaintext token. The user
may use `DYMMER_TOKEN` for that invocation.

`dym auth login --help` and `dym auth token-help` explain how to obtain a
token in English:

1. Sign in to `https://dymmer.com/keys`.
2. Select **Generate**, or **Regenerate** when intentionally replacing an
   existing token.
3. Copy and save the displayed token immediately; Dymmer will not display it
   again, and regeneration invalidates the prior token.

`dym auth status` reports only whether credentials are available and which
credential source will be used. `dym auth logout` deletes the keychain entry.

## HTTP client and API mapping

A single client owns base-URL construction, Bearer authentication, request
execution, JSON decoding, and normalized API errors. It must use HTTPS by
default and send human-readable diagnostics to stderr; command result data goes
only to stdout.

Initial endpoints:

| CLI command | HTTP endpoint |
| --- | --- |
| `domain <d> records list/create` | `GET` / `POST` `/zones/<d>/records` |
| `domain <d> records update/delete` | `PUT` / `DELETE` `/zones/<d>/records/<id>` |
| `domain <d> mailboxes list` | `GET /zones/<d>/mailboxes` |
| `domain <d> forwardings list` | `GET /zones/<d>/forwardings` |
| `project <p> secrets get` | `GET /hosts/secrets?project=<p>&env=<e>&deployment=<n>&format=<f>` |

The current server API has no endpoint that lists domains or retrieves an
individual DNS record. Those commands are intentionally deferred until the
service exposes them; the CLI must not pretend they are implemented.

The records API returns JSON records, supports a `type` filter for listing,
and accepts creation/update payloads beneath a `record` key. Field validation
will be based on the server's implemented record schema rather than guessed
client-side rules.

## Safety and output

Destructive record deletion asks for confirmation on an interactive terminal;
`--yes` is required to bypass it in scripts. Any other state-changing command
introduced later will use the same rule.

Secret values are intentionally written only to stdout. The CLI does not log
them, create temporary files, or invoke a pager. `--format dotenv` emits the
server's `.env` representation verbatim; users are responsible for redirecting
it to an appropriately protected file. Errors remain on stderr so pipelines
can consume stdout safely.

List and record output will be stable JSON in the first release, with optional
human-readable tables considered after the API shapes are fully covered. This
avoids an unstable display contract and preserves all response fields.

## Internal structure

The Go module has focused packages:

- `cmd/`: Cobra root command and resource subcommands.
- `internal/config`: non-secret settings and environment override resolution.
- `internal/credentials`: keychain interface and native-store implementation.
- `internal/api`: HTTP client, typed request/response models, and error mapping.
- `internal/commands`: command orchestration separated from API transport.

Interfaces isolate terminal input/output, credentials, and HTTP transport for
tests. Commands depend on services, rather than on concrete terminal or
keychain APIs.

## Error handling

Authentication errors tell the user to run `dym auth login` or provide
`DYMMER_TOKEN`; they never echo credential material. API validation responses
surface the server's field errors. A 404 preserves the API's ambiguity: the CLI
reports that the requested resource was not found or is inaccessible, without
claiming which is true. Network failures include the target host and a concise
retry suggestion.

## Testing

Unit tests cover command argument validation, prompt cancellation, environment
precedence, keychain failure, request construction, response decoding, and API
error translation. `httptest.Server` verifies each initial endpoint and its
Bearer header without sending traffic to Dymmer. Command integration tests use
fake credentials and a fake HTTP transport to assert stdout/stderr separation,
secret redaction, and delete confirmation behavior.

## Out of scope

- Browser-based sign-in or API token generation.
- Plaintext-token fallback storage.
- Mailbox and forwarding mutation operations, because the present API exposes
  only listing endpoints.
- Fabricated domain-list or record-get operations until the server publishes
  matching endpoints.
- Telemetry or any logging of response bodies.

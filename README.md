# dym

[![Tests](https://github.com/dymmer-code/dym/actions/workflows/test.yml/badge.svg)](https://github.com/dymmer-code/dym/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/dymmer-code/dym?sort=semver)](https://github.com/dymmer-code/dym/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/dymmer-code/dym.svg)](https://pkg.go.dev/github.com/dymmer-code/dym)
[![Go version](https://img.shields.io/github/go-mod/go-version/dymmer-code/dym)](go.mod)
[![License](https://img.shields.io/github/license/dymmer-code/dym)](LICENSE)

`dym` is a command-line client for the [Dymmer](https://dymmer.com) hosting API.

## Install

```sh
go install github.com/dymmer-code/dym@latest
```

## Documentation

- [`dym daemon`](docs/daemon.md): connect a host to Dymmer without SSH, with its [configuration file](docs/daemon-configuration.md) and [how to run it as a service](docs/daemon-service.md)
- [Extensions](docs/extensions.md): use `dym` as a generic client for any web service that speaks JSON

## Authentication

`dym` needs a Dymmer API token to talk to the API.

Sign in at https://dymmer.com/keys, select Generate or Regenerate, then copy the token immediately. Dymmer will not show it again; regenerating revokes the prior token.

Store the token securely in your OS keychain:

```sh
dym auth login
```

You'll be prompted to paste the token; it is saved to the system credential store and never written to disk in plaintext.

Other auth commands:

```sh
dym auth status       # show which credential source will be used, plus its scopes
dym auth logout       # remove the saved token from the keychain
dym auth token-help   # print instructions for obtaining a token
```

At https://dymmer.com/keys you can also create several named, scoped tokens
instead of the single full-access one -- e.g. one limited to DNS records on a
single domain for a CI server, so it can't touch the rest of the account.
`dym auth status` reports the scopes and any domain/project restriction of
whichever token is in use.

### Environment override

Setting `DYMMER_TOKEN` takes priority over any token saved in the keychain:

```sh
export DYMMER_TOKEN=your-token-here
```

## Command reference

### `dym auth`

| Command | Description |
| --- | --- |
| `dym auth login` | Prompt for a token and save it to the OS keychain |
| `dym auth logout` | Remove the saved token from the OS keychain |
| `dym auth status` | Show which credential source (keychain or `DYMMER_TOKEN`) will be used, and the token's scopes/restrictions |
| `dym auth token-help` | Print instructions for obtaining a token |

### `dym domain <domain> records`

| Command | Description |
| --- | --- |
| `dym domain <domain> records list [--type <type>] [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | List DNS records, optionally filtered by type |
| `dym domain <domain> records create --type <type> [flags] [--select fields] [-o table\|json\|csv\|tsv]` | Create a DNS record |
| `dym domain <domain> records update <id> --type <type> [flags] [--select fields] [-o table\|json\|csv\|tsv]` | Update a DNS record (`--type` must always be resent) |
| `dym domain <domain> records delete <id> [--yes] [--select fields] [-o table\|json\|csv\|tsv]` | Delete a DNS record (prompts for confirmation unless `--yes` is given or stdin isn't a terminal, in which case `--yes` is required) |

Content flags accepted by `create` and `update` (only the ones you set are sent to the server):

`--name`, `--ttl`, `--ip`, `--ipv6`, `--alias`, `--mail-provider`, `--priority`, `--ns`, `--value`, `--port`, `--weight`, `--target`

### `dym domain <domain> identities`

An address (an *identity*) can send with its own password, let other addresses send as it, and receive in a mailbox or forward; a mailbox and a forwarding are never both. `<address>` is a name, `name@domain` or an id. Lists show the first 8 characters of an id; any beginning of it that is not ambiguous works, as in git (so does `<id>` of `storages`).

| Command | Description |
| --- | --- |
| `dym domain <domain> identities list [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | List the addresses, how each one sends and where it receives |
| `dym domain <domain> identities add <name> [--password-stdin \| --ask-password] [--mailbox [--from-storage <id>] \| --forward a@x.com,b@x.com \| --no-receive] [--enabled=false]` | Add an address. A mailbox needs a password |
| `dym domain <domain> identities edit <address> [--rename <name> [--update-forwardings]] [--enabled=bool] [--password-stdin \| --ask-password \| --remove-password] [--mailbox \| --forward ... \| --no-receive]` | Change only what is named. Renaming keeps the mailbox, its mail and the permissions to send as it; `--update-forwardings` makes the forwardings of the zone that sent to the old address send to the new one |
| `dym domain <domain> identities delete <address> [--yes]` | Delete an address for good. The mail of its mailbox, if any, is kept for a while (see `storages`) |
| `dym domain <domain> identities send-as add\|remove <address> <credential>` | Let an address with a password (of this zone or of another of yours, `name@other.org`) send as `<address>`, or stop it |

The password is read from the standard input (`--password-stdin`) or asked twice on the terminal without echo (`--ask-password`); there is deliberately no flag that takes it as an argument, because it would show in the list of processes. Output never includes the password hash.

`mailboxes` and `forwardings` no longer exist (the API answers `410 update dym`): use `identities`.

### `dym domain <domain> storages`

When a mailbox is deleted, or its address stops being a mailbox, its mail is kept for a while as a *deleted mailbox*; it still counts in your limit until it is gone.

| Command | Description |
| --- | --- |
| `dym domain <domain> storages list [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | List the deleted mailboxes that can be recovered, with the days left |
| `dym domain <domain> storages attach <id> --as <name> [--password-stdin \| --ask-password]` | Recover it, with its mail, into a new address with that name |
| `dym domain <domain> storages attach <id> --to <address>` | Give it to an existing address that has no mailbox and does not forward (administrators only) |
| `dym domain <domain> storages delete <id> [--yes]` | Delete its mail now, for good, and free its place |

### `dym project <project> apps`

`dym project <project> apps list --env <env> [--deployment <name>] [-o table|json|csv|tsv]` lists apps and their hosts. The environment defaults to `dev`. Table/CSV/TSV output has one row per host with its app, status and ports; JSON retains the app and its nested `hosts` configuration. Apps without hosts appear as drafts. Requires the `deployment:read` API token scope and access to the project. Commands and secrets are never included; shared secrets remain available through `secrets get`.

### `dym project <project> secrets`

| Command | Description |
| --- | --- |
| `dym project <project> secrets get [--env <env>] [--deployment <name>] [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv\|dotenv]` | Fetch secrets for a project |

`--env` defaults to `dev`. `--deployment` is optional. `--output`/`-o` accepts `table` (default, a two-column KEY/VALUE table), `json` (an array of secret entries), `csv`/`tsv` (see below), or `dotenv` (the raw `.env`-formatted body). Secrets are written to stdout only; errors are written to stderr only. `--filter`/`--select` cannot be combined with `--output dotenv` (that output is the server's raw text, so field-level filtering/projection has no meaning there); using either with `--output dotenv` is an error, checked before any API call. `--filter`/`--select` combine fine with `--output csv`/`--output tsv`, same as with `table`/`json`.

### `dym daemon`

Keeps a host connected to Dymmer without SSH: the host opens the connection, so it works behind NAT or a firewall, and
Dymmer needs no key, no open port and no access to the host. Pair the host once with `dym auth daemon` (it shows a PIN
that you type in Dymmer), then run `dym daemon` as a service.

| Command | Description |
| --- | --- |
| `dym auth daemon [--server <address>] [--state <file>]` | Pair this host with Dymmer with a one-time PIN |
| `dym daemon [--server <address>] [--state <file>] [--config <file>] [--check-config]` | Keep this host connected; answer what Dymmer asks by name |

The daemon never accepts a script or a command line from Dymmer. It answers a fixed set of operations (measurements,
the status of a service) and, only for what the owner of the host declares in a configuration file, changes to services,
named actions and reading or following logs. It runs as **whoever starts it**: use a user of its own, never root.

- [`dym daemon`: what it does, quick start, every option](docs/daemon.md)
- [The configuration file](docs/daemon-configuration.md): services, Docker Compose projects, actions, logs and `sudo`
- [Running it as a service](docs/daemon-service.md): which user, systemd, FreeBSD, and how to do it as a normal user without root

### `dym ext`

| Command | Description |
| --- | --- |
| `dym ext list` | List extensions declared in the extensions file |
| `dym ext <name> [params...] [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | Run a user-defined extension (tabular mode, when `response` is omitted) |
| `dym ext <name> [params...] [--to <file>]... [--append-to <file>]...` | Run a user-defined extension with `response` templates |

See [Extensions](#extensions) below for the config file and schema.

## Output

Every command that prints API results defaults to a human-readable table on stdout. Pass `--output json` (`-o json`) to get JSON instead (byte-for-byte the same shape whether the result is a list or a single created/updated/deleted record); `secrets get` also accepts `--output dotenv` for the raw `.env`-formatted body. Empty lists print a `No <resource> found.` message instead of an empty table. Errors are written to stderr, and the process exits non-zero on failure.

`--output csv` and `--output tsv` print the same columns as `table`, comma- or tab-separated, with values quoted per RFC 4180 when they contain the delimiter, a `"`, or a newline. Unlike `table`, they print **no header row and no messages at all** — not even the `No <resource> found.` message `table` prints for an empty result; an empty result with `--output csv`/`--output tsv` writes zero bytes.

### Narrowing and projecting output: `--filter` and `--select`

`--filter` and `--select` are simple, dependency-free field tools — equality filtering and column projection, not a full query language (no `jq`/JMESPath-style expressions, no `<`/`>`/`contains`).

Field names are the API's real JSON field names, not always the same as the CLI's create/update flag names. Notably: a DNS record's TTL flag is `--ttl` but its field name is `time_to_live`; a record's type-specific data (IP address, alias target, etc.) lives under `content`, accessed with a dot, e.g. `content.ip`, `content.value`, `content.mail_provider` (same key names used for the `--ip`/`--value`/`--mail-provider` flags). The full field sets:

- `records`: `id`, `type`, `host`, `time_to_live`, `content.<key>`
- `identities`: `id`, `username`, `enabled`, `has_password`, `mailbox.quota_tier`, `mailbox.quota_used_mb`, `mailbox.storage_key`, `forwarding.destination`, `authorizations` (the password hash is never kept; mail servers read it from `/api/v1/zones/<domain>/identities` with a token scoped `identity:read`)
- `storages`: `id`, `name`, `abandoned_at`, `purge_after`, `days_left`, `usage_bytes`
- `secrets get`: `key`, `value`, `comments`, `removed`

**`--filter field=value`** (repeatable, AND-combined) keeps only rows where `field` equals `value`; `--filter field!=value` keeps rows where it doesn't. Repeat the flag to combine multiple conditions, e.g. `--filter type=A --filter host=www` keeps only rows matching both. Comparison is by string representation on both sides — no numeric/boolean-aware comparison. For `identities`' `forwarding.destination` field (a list of addresses), `--filter destination=someone@example.com` matches if that address is *one of* the destinations, not if the whole list equals it. A field that doesn't resolve on a row (a typo, or a content key that record type doesn't have) simply excludes that row — it's not an error. A malformed `--filter` value (missing `=` entirely) is an error, reported before any API call. `--filter` only applies to commands that list rows: `records list`, `identities list`, `storages list`, `secrets get`.

**`--select field1,field2,...`** (comma-separated, single flag) reduces which fields are shown, in the given order — for both `--output table` (columns become the selected fields, headers uppercased, e.g. `content.ip` -> `CONTENT.IP`) and `--output json` (each object contains exactly those keys, in that order; a field that doesn't resolve on a row renders as an empty table cell or JSON `null`). `--select` applies everywhere a command renders a result: the list commands above, plus `records create`/`update`/`delete` (projecting the fields of the single created/updated/deleted record). Leaving `--select` unset keeps the existing fixed-column table / full-object JSON output unchanged.

## Extensions

A step-by-step guide, with what it can and cannot do, is in [docs/extensions.md](docs/extensions.md). With `auth: bearer` and no `token`, the token sent is your Dymmer token: `dym` warns on the standard error whenever the address is not the Dymmer API.

`dym` ships with **zero** extensions by default. `dym ext` is a generic escape hatch: a YAML config file where you declare arbitrary HTTP calls against Dymmer (or Dymmer-adjacent) endpoints `dym` doesn't have a built-in command for, and `dym` turns each declared entry into a real subcommand — `dym ext <name>` — with no code changes required.

### Config file location

By default `dym` looks for the extensions file at your OS's standard config directory:

- Linux: `$XDG_CONFIG_HOME/dym/extensions.yaml` (or `~/.config/dym/extensions.yaml`)
- macOS: `~/Library/Application Support/dym/extensions.yaml`
- Windows: `%AppData%\dym\extensions.yaml`

Override order (highest priority first):

1. `--extensions-file <path>` (or `-e <path>`) flag, anywhere after `dym ext` on the command line
2. `DYM_EXTENSIONS_FILE` environment variable
3. the default location above

If no extensions file exists, `dym ext list` says so and every other `dym ext <name>` invocation behaves like any other unknown command — nothing breaks, and every other `dym` command is unaffected (in particular, `dym ext` never reads this file at all until you actually run `dym ext ...`). If the file exists but fails to parse as YAML (or uses an unrecognized key — the schema is validated strictly), `dym ext` reports that error clearly instead of silently doing nothing. If an individual extension within an otherwise-valid file fails its own validation (bad `method`, missing `url`, etc.), only that extension is skipped: the rest of the file's extensions still load and work, `dym ext list` reports how many were skipped and why, and asking for a skipped extension by name (`dym ext <name>`) names the specific reason rather than looking like a typo.

### Schema

```yaml
extensions:
  mail-domains:
    description: "Authorized mail-server domains (internal endpoint, no auth)"
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    method: GET                # optional, default GET
    auth: none                 # optional, default "none"; or "bearer"
    params: []                 # optional, default []; declared positional parameter names, in order

  create-dkim-txt:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    method: POST
    auth: bearer                # token defaults to "{{.DymmerToken}}" when unset
    params: [domain, value]
    request_template: |
      {"record":{"host":"mail._domainkey","type":"TXT","content_value":"{{.Args.value}}"}}
```

Field reference (all under a top-level `extensions:` map keyed by extension name):

| Field | Required | Description |
| --- | --- | --- |
| `description` | no | Shown by `dym ext list` |
| `url` | **yes** | A Go [`text/template`](https://pkg.go.dev/text/template), rendered before every call against `{BaseURL, DymmerToken, Args}` |
| `method` | no | `GET` (default), `POST`, `PUT`, `PATCH`, or `DELETE` (case-insensitive) |
| `auth` | no | `none` (default) or `bearer` |
| `token` | no | A template for the bearer token; only meaningful when `auth: bearer`, defaults to `"{{.DymmerToken}}"`; an error if set alongside `auth: none` |
| `params` | no | Positional parameter names, in order; the generated subcommand requires exactly this many args, and its `--help` shows them, e.g. `dym ext zone-txt-records <domain>` |
| `request_template` | no | A Go template rendered against the same data as `url`, sent as the request body; must render to valid JSON; only valid when `method` is not `GET`; when set, `Content-Type: application/json` is set automatically |
| `response` | no | A list of template entries (`- template: ...`) rendered against `{Args, Body}` (`Body` is the decoded JSON response; `Args` is the same param map `url`/`token`/`request_template` see). When set, `--to` and `--append-to` are registered to route template outputs to files; `--filter`/`--select`/`--output` are **not** registered. When omitted, output is rendered as table/json/csv/tsv directly from the response JSON array |

Inside `url`, `token`, and `request_template`, templates render against:

```go
{
  BaseURL     string            // the Dymmer API base URL
  DymmerToken string            // resolved lazily, only when auth: bearer; empty otherwise
  Args        map[string]string // declared params, keyed by name, e.g. {{.Args.domain}}
}
```

`response` templates instead render against:

```go
{
  Args map[string]string // exactly the same params map as above, e.g. {{.Args.domain}}
  Body any               // the decoded JSON response body
}
```

so `{"domains":["a.com"]}` is addressed as `{{range .Body.domains}}{{.}}\n{{end}}`, not bare `{{range .domains}}`. Splitting `Body` out from the top level like this is what lets a response template combine response data with request-time params the server itself never echoes back — see the worked examples below.

Template functions available in `request_template` and `response`:
- `toJson`: marshals data as a compact JSON string (e.g. `{{.Body | toJson}}` or `{{.Args | toJson}}`).
- `toPrettyJson`: marshals data as formatted, two-space indented JSON (e.g. `{{.Body | toPrettyJson}}`).

When `response` is omitted, the decoded response array becomes rows for `--filter`/`--select`/`--output table|json|csv|tsv`, same as `records list`/`mailboxes list`/etc.: object elements become rows as-is; scalar elements (a plain array of strings, say) are wrapped as `{"value": <elem>}`. With no `--select`, the default columns are the sorted union of every row's keys.

### Worked examples

**GET with default tabular output** — list domains from an internal, unauthenticated endpoint:

```yaml
extensions:
  mail-domains:
    description: "Authorized mail-server domains"
    url: "{{.BaseURL}}/internal/v1/mail_servers/domains"
    auth: none
```

```sh
$ dym ext mail-domains
VALUE
a.example.com
b.example.com
```

**POST with `request_template`** — create a DKIM TXT record, authenticated with your Dymmer token:

```yaml
extensions:
  create-dkim-txt:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/records"
    method: POST
    auth: bearer
    params: [domain, value]
    request_template: |
      {"record":{"host":"mail._domainkey","type":"TXT","content_value":"{{.Args.value}}"}}
```

```sh
dym ext create-dkim-txt example.com "v=DKIM1; k=rsa; p=..."
```

**`response` template combining `Args` and `Body`** — a custom line format (a Dovecot passwd-file) where the domain name itself only ever exists on the request side. It reads the mail identities endpoint, which needs a token with the `identity:read` scope:

```yaml
extensions:
  mailbox-passwd-lines:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/identities"
    auth: bearer
    params: [domain]
    response:
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.identities}}{{if and .enabled .password_hash}}{{.username}}@{{$domain}}:{{.password_hash}}::::/mail/{{$domain}}/{{.username}}::
          {{end}}{{end}}
```

```sh
dym ext mailbox-passwd-lines example.com
```

**`response` with multiple outputs to files** — generating two different Postfix configuration maps from a single API call:

```yaml
extensions:
  mailbox-lines:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/identities"
    auth: bearer
    params: [domain]
    response:
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.identities}}{{if and .enabled .mailbox}}{{.username}}@{{$domain}}	OK
          {{end}}{{end}}
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.identities}}{{if and .enabled .mailbox}}{{.username}}@{{$domain}}	{{.username}}@{{$domain}}
          {{end}}{{end}}
```

```sh
dym ext mailbox-lines example.com --to /etc/postfix/virtual_mailbox_maps --append-to /etc/postfix/sender_login_maps
```

All examples are illustrative — write your own `extensions.yaml` for whatever internal or Dymmer-adjacent endpoints your workflow needs; `dym` has no bundled extensions.

### Register a local dymploy builder

After pairing the machine with `dym auth daemon`, an administrator who owns the
host can register its builder profile:

```sh
dym host builder register --config /etc/dymploy/config.yaml --token-owner dymploy
```

Authenticate dym normally with a token containing `builder:register`. This token
is only needed for registration; dymploy receives an independent profile secret.
The command requires the paired daemon state (`--state` overrides its location)
and the source IP must already be in that host's inventory. Use `--server` for a
non-default Dymmer instance; HTTPS is required except on loopback for local tests.
For a private CA, pass `--ca-file /path/to/ca.pem`; certificate and hostname
verification remain enabled.

The command reads a standalone YAML file, exports only the profile name, API URL
and repository policy, saves the returned secret with mode `0600`, and adds the
`control_plane` URL, builder ID and token file. Other local fields remain in the
file. The profile name defaults to `dymploy` (`--name`), and `--api-url` overrides
the address derived from `listen`. Local execution limits and credential paths
are never sent to Dymmer. Policy rules must satisfy Dymmer's exact-match contract.

The secret defaults to `<config>.dymmer-token`, or the existing absolute
`control_plane.token_file`. It inherits the configuration owner unless
`--token-owner` selects the service account (requires permission to change owner).
Run with permission to read the daemon state and update those files. Neither the
profile secret nor the registration token is printed. Reload/restart dymploy
explicitly after registration; this command does not restart services.

A repeat with the saved secret confirms the existing profile without rotating
its credential or changing its configuration in Dymmer. If updating the YAML
fails after saving the secret, rerun the same command. If the response is lost
or the secret cannot be saved, recover through explicit administrator credential
rotation; an existing profile never silently issues a replacement secret.
App acceptance in dymploy and GitHub App access remain separate requirements.

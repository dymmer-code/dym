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
dym auth status       # show which credential source will be used
dym auth logout       # remove the saved token from the keychain
dym auth token-help   # print instructions for obtaining a token
```

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
| `dym auth status` | Show which credential source (keychain or `DYMMER_TOKEN`) will be used |
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

### `dym domain <domain> mailboxes`

| Command | Description |
| --- | --- |
| `dym domain <domain> mailboxes list [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | List mailboxes |

### `dym domain <domain> forwardings`

| Command | Description |
| --- | --- |
| `dym domain <domain> forwardings list [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv]` | List mail forwardings |

### `dym project <project> secrets`

| Command | Description |
| --- | --- |
| `dym project <project> secrets get [--env <env>] [--deployment <name>] [--filter field=value]... [--select fields] [-o table\|json\|csv\|tsv\|dotenv]` | Fetch secrets for a project |

`--env` defaults to `dev`. `--deployment` is optional. `--output`/`-o` accepts `table` (default, a two-column KEY/VALUE table), `json` (an array of secret entries), `csv`/`tsv` (see below), or `dotenv` (the raw `.env`-formatted body). Secrets are written to stdout only; errors are written to stderr only. `--filter`/`--select` cannot be combined with `--output dotenv` (that output is the server's raw text, so field-level filtering/projection has no meaning there); using either with `--output dotenv` is an error, checked before any API call. `--filter`/`--select` combine fine with `--output csv`/`--output tsv`, same as with `table`/`json`.

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
- `mailboxes`: `username`, `enabled`, `password_md5`
- `forwardings`: `username`, `enabled`, `destination`
- `secrets get`: `key`, `value`, `comments`, `removed`

**`--filter field=value`** (repeatable, AND-combined) keeps only rows where `field` equals `value`; `--filter field!=value` keeps rows where it doesn't. Repeat the flag to combine multiple conditions, e.g. `--filter type=A --filter host=www` keeps only rows matching both. Comparison is by string representation on both sides — no numeric/boolean-aware comparison. For `forwardings`' `destination` field (a list of addresses), `--filter destination=someone@example.com` matches if that address is *one of* the destinations, not if the whole list equals it. A field that doesn't resolve on a row (a typo, or a content key that record type doesn't have) simply excludes that row — it's not an error. A malformed `--filter` value (missing `=` entirely) is an error, reported before any API call. `--filter` only applies to commands that list rows: `records list`, `mailboxes list`, `forwardings list`, `secrets get`.

**`--select field1,field2,...`** (comma-separated, single flag) reduces which fields are shown, in the given order — for both `--output table` (columns become the selected fields, headers uppercased, e.g. `content.ip` -> `CONTENT.IP`) and `--output json` (each object contains exactly those keys, in that order; a field that doesn't resolve on a row renders as an empty table cell or JSON `null`). `--select` applies everywhere a command renders a result: the four list commands above, plus `records create`/`update`/`delete` (projecting the fields of the single created/updated/deleted record). Leaving `--select` unset keeps the existing fixed-column table / full-object JSON output unchanged.

## Extensions

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

**`response` template combining `Args` and `Body`** — a custom line format for an endpoint `dym` already has a native command for (`dym domain <domain> mailboxes list`), where the domain name itself only ever exists on the request side:

```yaml
extensions:
  mailbox-passwd-lines:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/mailboxes"
    auth: bearer
    params: [domain]
    response:
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.mailboxes}}{{if .enabled}}{{.username}}@{{$domain}}:{{.password_md5}}::::/mail/{{$domain}}/{{.username}}::
          {{end}}{{end}}
```

```sh
dym ext mailbox-passwd-lines example.com
```

**`response` with multiple outputs to files** — generating two different Postfix configuration maps from a single API call:

```yaml
extensions:
  mailbox-lines:
    url: "{{.BaseURL}}/api/v1/zones/{{.Args.domain}}/mailboxes"
    auth: bearer
    params: [domain]
    response:
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.mailboxes}}{{if .enabled}}{{.username}}@{{$domain}}	OK
          {{end}}{{end}}
      - template: |-
          {{$domain := .Args.domain}}{{range .Body.mailboxes}}{{if .enabled}}{{.username}}@{{$domain}}	{{.username}}@{{$domain}}
          {{end}}{{end}}
```

```sh
dym ext mailbox-lines example.com --to /etc/postfix/virtual_mailbox_maps --append-to /etc/postfix/sender_login_maps
```

All examples are illustrative — write your own `extensions.yaml` for whatever internal or Dymmer-adjacent endpoints your workflow needs; `dym` has no bundled extensions.

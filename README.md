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
| `dym domain <domain> records list [--type <type>] [--filter field=value]... [--select fields] [-o table\|json]` | List DNS records, optionally filtered by type |
| `dym domain <domain> records create --type <type> [flags] [--select fields] [-o table\|json]` | Create a DNS record |
| `dym domain <domain> records update <id> --type <type> [flags] [--select fields] [-o table\|json]` | Update a DNS record (`--type` must always be resent) |
| `dym domain <domain> records delete <id> [--yes] [--select fields] [-o table\|json]` | Delete a DNS record (prompts for confirmation unless `--yes` is given or stdin isn't a terminal, in which case `--yes` is required) |

Content flags accepted by `create` and `update` (only the ones you set are sent to the server):

`--name`, `--ttl`, `--ip`, `--ipv6`, `--alias`, `--mail-provider`, `--priority`, `--ns`, `--value`, `--port`, `--weight`, `--target`

### `dym domain <domain> mailboxes`

| Command | Description |
| --- | --- |
| `dym domain <domain> mailboxes list [--filter field=value]... [--select fields] [-o table\|json]` | List mailboxes |

### `dym domain <domain> forwardings`

| Command | Description |
| --- | --- |
| `dym domain <domain> forwardings list [--filter field=value]... [--select fields] [-o table\|json]` | List mail forwardings |

### `dym project <project> secrets`

| Command | Description |
| --- | --- |
| `dym project <project> secrets get [--env <env>] [--deployment <name>] [--filter field=value]... [--select fields] [-o table\|json\|dotenv]` | Fetch secrets for a project |

`--env` defaults to `dev`. `--deployment` is optional. `--output`/`-o` accepts `table` (default, a two-column KEY/VALUE table), `json` (an array of secret entries), or `dotenv` (the raw `.env`-formatted body). Secrets are written to stdout only; errors are written to stderr only. `--filter`/`--select` cannot be combined with `--output dotenv` (that output is the server's raw text, so field-level filtering/projection has no meaning there); using either with `--output dotenv` is an error, checked before any API call.

## Output

Every command that prints API results defaults to a human-readable table on stdout. Pass `--output json` (`-o json`) to get JSON instead (byte-for-byte the same shape whether the result is a list or a single created/updated/deleted record); `secrets get` also accepts `--output dotenv` for the raw `.env`-formatted body. Empty lists print a `No <resource> found.` message instead of an empty table. Errors are written to stderr, and the process exits non-zero on failure.

### Narrowing and projecting output: `--filter` and `--select`

`--filter` and `--select` are simple, dependency-free field tools — equality filtering and column projection, not a full query language (no `jq`/JMESPath-style expressions, no `<`/`>`/`contains`).

Field names are the API's real JSON field names, not always the same as the CLI's create/update flag names. Notably: a DNS record's TTL flag is `--ttl` but its field name is `time_to_live`; a record's type-specific data (IP address, alias target, etc.) lives under `content`, accessed with a dot, e.g. `content.ip`, `content.value`, `content.mail_provider` (same key names used for the `--ip`/`--value`/`--mail-provider` flags). The full field sets:

- `records`: `id`, `type`, `host`, `time_to_live`, `content.<key>`
- `mailboxes`: `username`, `enabled`, `password_md5`
- `forwardings`: `username`, `enabled`, `destination`
- `secrets get`: `key`, `value`, `comments`, `removed`

**`--filter field=value`** (repeatable, AND-combined) keeps only rows where `field` equals `value`; `--filter field!=value` keeps rows where it doesn't. Repeat the flag to combine multiple conditions, e.g. `--filter type=A --filter host=www` keeps only rows matching both. Comparison is by string representation on both sides — no numeric/boolean-aware comparison. For `forwardings`' `destination` field (a list of addresses), `--filter destination=someone@example.com` matches if that address is *one of* the destinations, not if the whole list equals it. A field that doesn't resolve on a row (a typo, or a content key that record type doesn't have) simply excludes that row — it's not an error. A malformed `--filter` value (missing `=` entirely) is an error, reported before any API call. `--filter` only applies to commands that list rows: `records list`, `mailboxes list`, `forwardings list`, `secrets get`.

**`--select field1,field2,...`** (comma-separated, single flag) reduces which fields are shown, in the given order — for both `--output table` (columns become the selected fields, headers uppercased, e.g. `content.ip` -> `CONTENT.IP`) and `--output json` (each object contains exactly those keys, in that order; a field that doesn't resolve on a row renders as an empty table cell or JSON `null`). `--select` applies everywhere a command renders a result: the four list commands above, plus `records create`/`update`/`delete` (projecting the fields of the single created/updated/deleted record). Leaving `--select` unset keeps the existing fixed-column table / full-object JSON output unchanged.

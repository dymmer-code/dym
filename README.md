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
| `dym domain <domain> records list [--type <type>]` | List DNS records, optionally filtered by type |
| `dym domain <domain> records create --type <type> [flags]` | Create a DNS record |
| `dym domain <domain> records update <id> --type <type> [flags]` | Update a DNS record (`--type` must always be resent) |
| `dym domain <domain> records delete <id> [--yes]` | Delete a DNS record (prompts for confirmation unless `--yes` is given or stdin isn't a terminal, in which case `--yes` is required) |

Content flags accepted by `create` and `update` (only the ones you set are sent to the server):

`--name`, `--ttl`, `--ip`, `--ipv6`, `--alias`, `--mail-provider`, `--priority`, `--ns`, `--value`, `--port`, `--weight`, `--target`

### `dym domain <domain> mailboxes`

| Command | Description |
| --- | --- |
| `dym domain <domain> mailboxes list` | List mailboxes |

### `dym domain <domain> forwardings`

| Command | Description |
| --- | --- |
| `dym domain <domain> forwardings list` | List mail forwardings |

### `dym project <project> secrets`

| Command | Description |
| --- | --- |
| `dym project <project> secrets get [--env <env>] [--deployment <name>] [--format json\|dotenv]` | Fetch secrets for a project |

`--env` defaults to `dev`. `--deployment` is optional. `--format` accepts `json` (default, an array of secret entries) or `dotenv` (the raw `.env`-formatted body). Secrets are written to stdout only; errors are written to stderr only.

## Output

Commands that print API results write JSON (or, for `secrets get --format dotenv`, raw dotenv text) to stdout. Errors are written to stderr, and the process exits non-zero on failure.

# Use `dym` as a generic client for web services: extensions

Besides its own commands, `dym` can turn a few lines of YAML into a command that calls **any HTTP service that speaks
JSON**: a service of yours, a webhook, a public API. You declare the URL, the method, the parameters and what to do with
the answer, and `dym ext <name>` runs it. Nothing is compiled and nothing is installed; the declarations live in one
file.

It is meant for small, repeatable calls you want in a terminal, a script, cron or a CI job, with the same output options
(`table`, `json`, `csv`, `tsv`, `--filter`, `--select`) as the rest of `dym`. It is **not** a full HTTP client: see
[What it cannot do](#what-it-cannot-do) before you build on it.

The complete schema is in the [README](../README.md#extensions); this page is the guide.

## A first extension in two minutes

Create the file (the path is explained [below](#where-the-file-is)):

```sh
mkdir -p ~/.config/dym
cat > ~/.config/dym/extensions.yaml <<'EOF'
extensions:
  releases:
    description: "Releases of a GitHub repository"
    url: "https://api.github.com/repos/{{.Args.repo}}/releases"
    params: [repo]
EOF
```

```sh
dym ext list
dym ext releases dymmer-code/dym --select tag_name,published_at
```

```
TAG_NAME  PUBLISHED_AT
v0.14.1   2026-10-17T21:52:31Z
v0.14.0   2026-10-17T21:45:26Z
v0.13.0   2026-10-17T02:31:31Z
…
```

What happened: `releases` became a subcommand with one required argument, `<repo>`. `{{.Args.repo}}` is replaced by what
you typed, the call is a `GET` (the default), and because the answer is a JSON array each element became a row.
`--select`, `--filter tag_name=v0.14.0` and `-o json|csv|tsv` work like in `records list`.

## Where the file is

By order of priority:

1. `dym ext --extensions-file <path> <name> …` (or `-e <path>`)
2. the environment variable `DYM_EXTENSIONS_FILE`
3. the default: `$XDG_CONFIG_HOME/dym/extensions.yaml` or `~/.config/dym/extensions.yaml` on Linux and FreeBSD,
   `~/Library/Application Support/dym/extensions.yaml` on macOS, `%AppData%\dym\extensions.yaml` on Windows

`dym` reads the file only when you run `dym ext …`, so a broken file never affects any other command. `dym ext list`
shows the extensions and, if some were skipped because they are invalid, how many and why. A wrong key is an error
(the schema is strict), and an invalid extension does not stop the others from loading.

## Building a call

### The URL and the parameters

`url` is a [Go template](https://pkg.go.dev/text/template). It can point to **any** address, not only to Dymmer. The
declared `params` are positional arguments, required, in order; the command refuses to run with another number.

```yaml
extensions:
  search-issues:
    url: "https://api.example.com/search?q={{urlquery .Args.query}}&limit={{.Args.limit}}"
    params: [query, limit]
```

```sh
dym ext search-issues "disk full" 20
```

Escape what goes in a query string with `{{urlquery …}}`: a template does not do it for you. `{{.BaseURL}}` is the
address of the Dymmer API, which is what the extensions for Dymmer endpoints use (`{{.BaseURL}}/api/v1/…`).

### The method and the body

`method` is `GET` (default), `POST`, `PUT`, `PATCH` or `DELETE`. For anything but `GET` you can add a
`request_template`, rendered with the same data, which has to produce **valid JSON** (otherwise `dym` stops before
sending anything). The request goes with `Content-Type: application/json`.

Build values into the JSON with `toJson`, which quotes and escapes them, instead of putting quotes around them by hand:

```yaml
extensions:
  notify:
    description: "Post a message to a chat webhook"
    url: "https://chat.example.com/hooks/abc123"
    method: POST
    params: [text]
    request_template: |
      {"text": {{.Args.text | toJson}}, "channel": "ops"}
```

```sh
dym ext notify 'Backup finished: 3 files, "ok"'
```

With `{"text": "{{.Args.text}}"}` a message with a quote in it would produce invalid JSON, or a different JSON from the one
you meant.

### Authentication

`auth` is `none` (default) or `bearer`, which sends `Authorization: Bearer <token>`.

> **Careful: the default token is your Dymmer token.** With `auth: bearer` and no `token:`, `dym` sends the token of your
> Dymmer account. That is right for Dymmer addresses (`{{.BaseURL}}/…`). For any other address it means handing the
> token to whoever runs that service, who could then act as your account in Dymmer. `dym` does not stop you, because you
> wrote the extension and may mean it, but it **warns you on the standard error every time**:
>
> ```
> warning: extension "elsewhere" sends your Dymmer token to api.example.com, which is not the Dymmer API (dymmer.com).
>          Whoever runs that service can use the token to act as your Dymmer account. If the service needs a token,
>          give it its own with `token:` in the extension instead of the default.
> ```
>
> The call still goes ahead and the token is never shown. An extension with its own `token:` (below) or with
> `auth: none` never warns.

For a different service, give the token yourself with `token`, which is also a template:

```yaml
extensions:
  create-ticket:
    url: "https://tickets.example.com/api/tickets"
    method: POST
    auth: bearer
    token: "{{.Args.token}}"
    params: [token, title]
    request_template: |
      {"title": {{.Args.title | toJson}}}
```

```sh
dym ext create-ticket "$TICKETS_TOKEN" "Printer on fire"
```

The token is then a command-line argument, which means it is visible to other users of the machine (`ps`) while the
command runs and may end up in your shell history if you type it. Keep it in a variable of a script, not on the line
you type, and do not use a token with more rights than the call needs. The extension file itself never needs to hold a
secret, and error messages hide the real URL and token.

### What you get back

**A list of things** (the answer is a JSON array): leave `response` out. Each element is a row; a list of plain strings
becomes a one-column table. Then `--output table|json|csv|tsv`, `--filter field=value` and `--select a,b.c` work, with
`.` to reach inside objects (`--select owner.login`). `csv` and `tsv` print no header and nothing at all when the list
is empty, which is what a script wants.

**Anything else** (an object, text you need to shape): add `response` templates. They render against `{Args, Body}`,
where `Body` is the decoded JSON of the answer. The first template goes to the standard output:

```yaml
extensions:
  status:
    url: "https://api.example.com/status"
    response:
      - template: |
          {{.Body.service}}: {{.Body.state}} ({{len .Body.checks}} checks)
```

```sh
dym ext status
# api: ok (12 checks)
```

A template prints **exactly what it renders**. `dym` adds no newline, so end the template with one: a block written with
`|` keeps the final line break, `|-` removes it (which is what you want when you build a file line by line and control
every line break yourself).

Print the whole answer, nicely, with `{{.Body | toPrettyJson}}` (or compact, `toJson`).

**Files.** With several `response` templates, send each to a file with `--to <file>` (overwrites) or `--append-to <file>`
(appends), **one flag per template, in order**. This is how a single call can generate several configuration files:

```sh
dym ext mailbox-lines example.com --to /etc/postfix/virtual_mailbox_maps --append-to /etc/postfix/sender_login_maps
```

If you give `response`, `--output`, `--filter` and `--select` are not available: the templates own the format.

## Using it in scripts

- **Exit status:** 0 when the service answers with a 2xx code and the output was written; otherwise 1, with the reason
  on the standard error. A non-2xx answer is reported with its code and the first 500 bytes of the body. Errors go to
  the standard error and results to the standard output, so `> file` never captures an error.
- **Nothing is retried**, so an extension that does a `POST` runs once per command.
- **Output for programs:** `-o json` and `jq`, or `-o csv`/`-o tsv` and `cut`/`awk`.

```sh
# the tags published before a date, one per line
dym ext releases dymmer-code/dym -o tsv --select tag_name,published_at | awk -F'\t' '$2 < "2026-10-17" {print $1}'
```

```cron
# post the result of a check to a chat every morning
0 8 * * *  /home/alice/bin/morning-check.sh | xargs -d '\n' -I{} dym ext notify "{}"
```

## What it cannot do

Know these limits before relying on it:

- **JSON only.** The answer has to be JSON. HTML, XML or plain text are an error, and so is an empty answer unless the
  extension has `response` templates (then `Body` is empty and the templates decide what to print).
- **No custom headers.** The only headers are `Content-Type` (when there is a body) and `Authorization: Bearer`.
  A service that wants an `X-API-Key`, basic authentication, cookies or a signature cannot be called.
- **Bodies are JSON** and built from the `request_template`; there is no form encoding, file upload or streaming.
- **No access to the environment from a template.** Secrets arrive as parameters (see above), which is why the
  warning about command lines matters.
- **One call per command.** No loops, no pagination, no retries, no chaining one answer into the next call. Do that in
  the shell around it.
- **Limits:** 30 seconds per call, and an answer of more than 512 KiB is refused rather than buffered.
- **The name** of an extension cannot be `list`, `help` or `completion`, and cannot contain spaces.

When you need more than this, use `curl` or a real script; `dym ext` is for the calls where one short declaration is
enough.

## Keeping extension files safe

An extension file decides which addresses `dym` calls and what it sends, so treat it like a script: keep it in your
home, owned by you, and do not run `dym ext -e` on a file you did not read. `dym ext list` never runs anything. Errors
print the address with the token hidden, but the address itself is shown.

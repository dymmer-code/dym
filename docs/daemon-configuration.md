# The configuration file of `dym daemon`

By default `dym daemon` lets Dymmer **read measurements and ask the status of a service**, and nothing else. Everything
that changes the host, and everything that reads more than measurements, has to be written by the owner of the host in
this file. It is the boundary between what Dymmer can ask and what the host agrees to do.

See [`dym daemon`](daemon.md) for the program and [Running it as a service](daemon-service.md) for how to start it.

## Where it is, and when it is read

| User that runs the daemon | Default path |
| --- | --- |
| root | `/etc/dym/daemon.yaml` |
| any other user, Linux and FreeBSD | `$XDG_CONFIG_HOME/dym/daemon.yaml`, or `~/.config/dym/daemon.yaml` |
| any other user, macOS | `~/Library/Application Support/dym/daemon.yaml` |

`--config <file>` chooses another path. The file is **optional**: without it, nothing that changes the host can be
asked.

The file is **read again for every order** that needs it, so a change takes effect without restarting the daemon. A
file with a mistake is **refused as a whole** and every order that needs the configuration fails with the reason, so a
typo can never allow more than what you wrote. Check a file before putting it in place:

```sh
dym daemon --check-config --config ./daemon.yaml
# ./daemon.yaml is valid
```

Unknown keys are mistakes too (`servises:` is refused, not ignored).

> **The file is what you trust.** Whoever can edit it decides what the daemon runs. Keep it owned by you (or by root)
> and, if the daemon runs as a different user, readable but not writable by that user (`0640`, group of the daemon).

## The whole file

```yaml
privilege: [sudo, -n]        # optional: put before the commands that change something

services:                    # what Dymmer may start, stop, restart or reload
  systemctl: [nginx, myapp]
  service:   [cron]
  init_d:    [legacy-app]
  docker:    [web]
  pm2:       [api]
  supervisord: [worker]

compose:                     # docker compose projects
  - path: /srv/app
    services: [web, worker]

actions:                     # commands that have a name
  rebuild-queues:
    command: [/home/alice/bin/rebuild-queues.sh]
    dir: /home/alice
    env: {MODE: prod}
    timeout: 5m
    privileged: false

logs:                        # the only things that can be read
  files: [/var/log/nginx/access.log]
  units: [nginx]
```

Every section is optional.

## Names

A service, container, process, unit or action name is made of **letters, digits and `. _ @ : -`**, up to 128
characters, and **never starts with a dash**. Anything else is refused before anything runs. So a name can never be read
as an option of the program it is given to (`--now`, `-f`), or carry a space, a `;` or a `$(…)`.

## `privilege`

A list of words that is put **in front of** every command that changes something: starting, stopping, restarting or
reloading a service, and the actions marked `privileged: true`. Typically `[sudo, -n]` (`-n`: never ask for a
password; fail instead).

```yaml
privilege: [sudo, -n]
```

The commands are built exactly as `privilege` + the command, with nothing added. For `systemctl: [nginx]` and a restart
the daemon runs `sudo -n systemctl restart nginx`, and that is the line your `sudoers` rule has to allow:

```
# /etc/sudoers.d/dym-daemon  (check it with: visudo -cf /etc/sudoers.d/dym-daemon)
alice ALL=(root) NOPASSWD: /usr/bin/systemctl restart nginx
```

Write one exact line per thing you want to allow, never `ALL`. Without `privilege`, those commands run as the daemon's
own user, which is what you want when the service belongs to that user (for example Docker containers of the same user).
The status of a service never uses `privilege`.

## `services`

For each manager, the names Dymmer may **start, stop, restart or reload**. The status of any service with a valid name
can be asked even if it is not listed.

| Manager | Allowed actions | Command that is run |
| --- | --- | --- |
| `systemctl` | start, stop, restart, reload | `systemctl <action> <name>` |
| `service` | start, stop, restart, reload | `service <name> <action>` |
| `init_d` | start, stop, restart | `/etc/init.d/<name> <action>` |
| `docker` | start, stop, restart | `docker <action> <name>` |
| `pm2` | start, stop, restart, reload | `pm2 <action> <name>` |
| `supervisord` | start, stop, restart | `supervisorctl <action> <name>` |

Each is run as a list of arguments, with `privilege` in front. A service that is not listed is refused with a message
that says so; one that is listed but whose command `sudo` does not allow is denied by `sudo`.

## `compose`

Docker Compose projects. **Every** operation on a project, including the status, needs it declared, because Compose
works in the directory of the project.

```yaml
compose:
  - path: /srv/app            # absolute, clean (no .. and no trailing slash)
    services: [web, worker]
```

| Operation | Command (run in `path`) |
| --- | --- |
| status | `docker compose ps <service>` |
| start | `docker compose up -d <service>` |
| stop, restart | `docker compose stop <service>` / `docker compose restart <service>` |

## `actions`

An action is **a name that points to a command you wrote**. In Dymmer, the command of an action of a project, or the
start/stop/restart/reload/status command of a *custom* deployment, is written as the **name** of the action; on a host
reached through the daemon Dymmer sends only that name.

```yaml
privilege: [sudo, -n]                               # needed because one action is privileged
actions:
  deploy-site:
    command: [/usr/local/bin/deploy-site, --prod]   # a list: program and arguments
    dir: /srv/site                                  # working directory (absolute); default: none
    env: {MODE: prod}                               # extra environment variables
    timeout: 5m                                     # default 1m, at most 30m
    privileged: true                                # run with the `privilege` prefix
```

| Key | Required | Meaning |
| --- | --- | --- |
| `command` | yes | The program and its arguments, as a **list**. Nothing is expanded: no `$HOME`, no `~`, no `*`, no pipes. Use absolute paths. |
| `dir` | no | Working directory. An absolute, clean path. |
| `env` | no | Variables added to the environment of the command. They win over the ones the daemon passes. |
| `timeout` | no | A duration such as `30s`, `5m`, `1h`. Default one minute, at most thirty. When it passes, the command and its children are killed and the action fails. |
| `privileged` | no | Put the `privilege` prefix in front. It needs `privilege` to be set, or the file is refused. |

What Dymmer shows as the result of the action is the **end of its output** (the last 2000 characters). An exit code
other than 0 is a failure, and the message includes the code and that output.

**An action that needs a shell** (pipes, `&&`, `$HOME`, a `cd`) goes inside a script, and the action calls the script:

```sh
#!/bin/sh
# /home/alice/bin/rebuild-queues.sh  (chmod +x)
cd "$HOME/docker/myapp" && docker compose exec -T php php bin/console queues:rebuild --env=prod
```

```yaml
actions:
  rebuild-queues:
    command: [/home/alice/bin/rebuild-queues.sh]
    timeout: 5m
```

In Dymmer, write `rebuild-queues` where the command goes. Inside the script `$HOME` is the home of the user that runs
the daemon.

### The environment of a command

A command does **not** inherit the environment of the daemon. It gets:

- `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` and `LC_ALL=C`;
- `HOME`: the home of the user that runs the daemon, or the temporary directory when that user has none;
- `USER`, `LOGNAME`, `XDG_RUNTIME_DIR`, `DOCKER_HOST`, `DOCKER_CONFIG` and `DBUS_SESSION_BUS_ADDRESS`, **only if the
  daemon has them** (rootless Docker and a user's D-Bus need them; set them in the service with `Environment=`);
- whatever the action declares in `env`.

Nothing else is passed on, so a secret in the environment of the service does not reach the scripts.

### Long actions

Dymmer waits for an action as long as its `timeout` allows (it never gives up before thirty minutes). Dymmer shows the
result **when the action ends**; there is no output while it runs. Ten or so seconds of margin are added so the action
ends by itself first.

## `logs`

Dymmer can read and follow only what is listed here.

```yaml
logs:
  files: [/var/log/nginx/access.log, /home/alice/app/storage/logs/prod.log]
  units: [nginx, myapp]
```

- `files`: absolute, clean paths, matched **exactly**. `/var/log/../etc/passwd` is not `/var/log/x` and is refused. A
  directory is not read. The user of the daemon must be able to read the file.
- `units`: systemd units whose journal is read with `journalctl -u <unit>`. The user must be allowed to read the journal
  (for a system user: be in the `systemd-journal` group, or use `adm` for files in `/var/log`).

Reading returns the last 15 lines that match, among the last 500, and following sends the last 10 lines and then the new
ones. The filter Dymmer sends is a regular expression ([RE2](https://github.com/google/re2/wiki/Syntax) syntax, up to
256 characters), applied to each line. At most **two** logs are followed at once per host, each for at most an hour.
A file that is rotated or truncated is followed through the change.

## Examples

**A web server and its logs**, managed with systemd by a user that can `sudo` those exact commands:

```yaml
privilege: [sudo, -n]
services:
  systemctl: [nginx]
logs:
  files: [/var/log/nginx/access.log, /var/log/nginx/error.log]
```

```
alice ALL=(root) NOPASSWD: /usr/bin/systemctl start nginx, /usr/bin/systemctl stop nginx, /usr/bin/systemctl restart nginx, /usr/bin/systemctl reload nginx
```

**An application of the user that runs the daemon**, with Docker Compose and no `sudo` at all:

```yaml
compose:
  - path: /home/alice/docker/myapp
    services: [php, worker]
actions:
  rebuild-queues:
    command: [/home/alice/bin/rebuild-queues.sh]
    timeout: 10m
logs:
  files: [/home/alice/docker/myapp/var/log/prod.log]
```

## Rules at a glance

- No file: Dymmer can only read measurements and the status of services.
- Declaring something never gives Dymmer a way to run arbitrary text: it can only call a **name** that is in the file.
- A file with any mistake is refused whole; the daemon does not guess.
- Commands are lists of arguments; nothing goes through a shell unless you wrote a script that does.
- `privilege` plus an exact `sudoers` line is the way to run something as root; never give the daemon's user general
  `sudo`.

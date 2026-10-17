# `dym daemon`: keep a host connected to Dymmer without SSH

`dym daemon` is a small program that runs on one of your hosts and keeps a connection open **from the host to
Dymmer**. Because the host opens the connection, it works behind NAT, a firewall or a provider that gives you no
public address, and Dymmer needs no SSH access, no open port and no key on the host.

Dymmer then shows the host as **on** while the daemon is connected, keeps its inventory and statistics up to date, and
can ask it for a short list of things by name: measurements, the status of a service, and whatever the owner of the
host has declared in the [configuration file](daemon-configuration.md).

This page explains what it does, how to set it up and every option of the commands. Other pages:

- [Configuration file](daemon-configuration.md): what Dymmer may change and read on the host.
- [Running it as a service](daemon-service.md): which user to use, systemd, FreeBSD, and how to do it without root.

## What it does, and what it never does

**It sends** to Dymmer, over the connection it opened:

- the **inventory** of the host: hostname, addresses, operating system, CPU and memory (when it changes);
- **statistics** every five minutes: load, memory in use, uptime, network counters and the use of each volume;
- a **heartbeat**, so that Dymmer knows the host is alive. If the connection drops, the daemon reconnects by itself,
  waiting a little longer after every failure (from one second up to a minute).

**It answers**, when Dymmer asks, a fixed set of *operations by name*:

| Operation | What it needs |
| --- | --- |
| Measurements: CPU, memory, disk, network, uptime (12 of them) | Nothing: they only read. |
| The status of a service: `systemctl`, `service`, `init.d`, Docker, Docker Compose, PM2, Supervisor | Nothing, for any valid name. Docker Compose needs the project to be declared. |
| Start, stop, restart or reload a service | The service must be declared in the configuration file. |
| An **action** by name | The action must be declared in the configuration file. |
| Reading a log, or following it live | The file or unit must be declared in the configuration file. |

**It never** accepts a script or a command line from Dymmer. Every operation is implemented inside the program, and
what Dymmer sends is only the name of one and its arguments. A name the program does not know is refused. An action is a
name that points to a command **the owner of the host wrote** in the configuration file; Dymmer cannot add to that
list, only call it. Without a configuration file, nothing that changes the host can be asked at all.

Commands run without a shell, with a time limit and a bounded output, and as **the user that started the daemon**: it
has no more rights than that user.

### Supported systems

The binary exists for Linux, FreeBSD, macOS and Windows. The inventory works everywhere; the operations above are
implemented for **Linux and FreeBSD**. A measurement that a system cannot give answers with a clear failure and never
with a made-up zero.

## Quick start

You need: a host with outbound HTTPS to Dymmer (`https://dymmer.com`, or your own server with `--server`), the
`dym` binary, and a Dymmer account.

**1. Put the binary on the host.** Download the file for your system from the
[releases page](https://github.com/dymmer-code/dym/releases) and check it against `checksums.txt`:

```sh
version=0.14.2   # the one you want
curl -fsSLO "https://github.com/dymmer-code/dym/releases/download/v${version}/dym_${version}_linux_amd64.tar.gz"
curl -fsSLO "https://github.com/dymmer-code/dym/releases/download/v${version}/checksums.txt"
sha256sum --ignore-missing -c checksums.txt        # on FreeBSD: sha256 -c or compare by hand
tar -xzf "dym_${version}_linux_amd64.tar.gz" dym
sudo install -m 0755 dym /usr/local/bin/dym         # without root: install -m 0755 dym ~/.local/bin/dym
```

The file names are `dym_<version>_<os>_<arch>.tar.gz` (`windows` ones are `.zip`), with `os` one of `linux`, `freebsd`,
`darwin`, `windows` and `arch` one of `amd64`, `arm64`. You can also build it with `go install github.com/dymmer-code/dym@latest`.

**2. Choose the user it will run as.** The daemon can do what that user can do, so read
[Which user to use](daemon-service.md#which-user-to-use) first. The short version: a user of its own, not root.

**3. Pair the host, once, as that user:**

```sh
dym auth daemon
```

It prints a PIN that is valid for **two minutes** and waits:

```
Pairing code: ABCD-EFGH
Enter it in Dymmer (Hosts, Add host, via dym) within 2m0s. Waiting...
```

In Dymmer, type the PIN in one of two places:

- **Hosts → Add with dym**, to create a new host with what the daemon reported; or
- the **edition of a host that already exists** (Hosts → edit): choose *Dym Daemon* as the way Dymmer reaches it and
  type the PIN. The host stays the same one and keeps its name, notes, permissions and projects.

When Dymmer accepts the PIN, the command prints `Paired: this machine is now the host …` and exits. The permanent
secret is kept in the *state file* (see below) with permissions `0600`, and it is never printed.

**4. Say what Dymmer may do on this host** (optional). Without it Dymmer can only read measurements and the status of
services. See the [configuration file](daemon-configuration.md).

**5. Run it as a service** so that it starts with the machine and restarts if it stops:
[Running it as a service](daemon-service.md). To try it first, in a terminal: `dym daemon`.

If the host is not paired yet, `dym daemon` does not fail: it says so once and waits, looking for the pairing every five
seconds, so a service can be enabled before the pairing is done.

## Command reference

### `dym auth daemon`

Pairs this host with Dymmer. It asks for a PIN, shows it, and waits until it is typed in Dymmer.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--server <address>` | `$DYM_SERVER`, or `https://dymmer.com` | Address of Dymmer. `https://` and `http://` are turned into the `wss://` / `ws://` address of the daemon socket. |
| `--state <file>` | see *Files* | Where the pairing is kept. |

If the PIN is not typed in time the command ends with an error and nothing is changed; run it again.

### `dym daemon`

Keeps the host connected until it receives `SIGINT` or `SIGTERM`. It logs to the standard error (systemd sends that to
the journal).

| Flag | Default | Meaning |
| --- | --- | --- |
| `--server <address>` | `$DYM_SERVER`, or `https://dymmer.com` | Address of Dymmer. |
| `--state <file>` | see *Files* | Where the pairing is kept. |
| `--config <file>` | see *Files* | The [configuration file](daemon-configuration.md). |
| `--check-config` | off | Read the configuration file, say whether it is valid, and exit. Nothing connects. |

### Environment

| Variable | Used for |
| --- | --- |
| `DYM_SERVER` | Address of Dymmer, when `--server` is not given. |

`dym daemon` and `dym auth daemon` do not use `DYMMER_TOKEN`: the host proves who it is with the secret in its state
file, not with an API token.

### Files

| File | For root | For any other user | Content |
| --- | --- | --- | --- |
| State | `/var/lib/dym/daemon.json` | `daemon.json` in the user's config directory | Host id and permanent secret. Mode `0600`, written atomically. |
| Configuration | `/etc/dym/daemon.yaml` | `daemon.yaml` in the user's config directory | What Dymmer may change and read. Optional. |

The user's config directory is `$XDG_CONFIG_HOME/dym` or `~/.config/dym` on Linux and FreeBSD, `~/Library/Application
Support/dym` on macOS and `%AppData%\dym` on Windows. A service often has no such directory (a system user without a
home): give `--state` and `--config` explicitly, as the examples in [Running it as a service](daemon-service.md) do.

## Checking that it works

- In Dymmer the host shows **on** within a few seconds of starting the daemon, and its statistics appear in a few
  minutes.
- `journalctl -u dym-daemon -f` (or the log of your service) shows `connected to Dymmer host_id=…`.
- After you edit the configuration file, check it before relying on it: `dym daemon --check-config --config /path/to/daemon.yaml`.

## Troubleshooting

| What you see | Why, and what to do |
| --- | --- |
| `this host is not paired yet: run dym auth daemon` | There is no state file where the daemon looks for it. Pair it, or point `--state` to the file you paired with. A service user usually needs an explicit `--state`. |
| `Dymmer does not accept this host: it was deleted there or its secret was replaced` | The host was deleted in Dymmer, or its way of reaching was changed to SSH or *disabled* (that invalidates the secret). Pair it again with `dym auth daemon`. |
| `disconnected from Dymmer … retry_in=…` repeating | No route to Dymmer, a proxy that closes websockets, or Dymmer is being updated. The daemon keeps trying by itself. Check that the host reaches `https://dymmer.com` and that nothing between closes idle websockets in under a minute. |
| The PIN is refused in Dymmer | It lasts two minutes, works once, and is the one **currently** shown. After five wrong tries Dymmer makes you wait ten minutes. Run `dym auth daemon` again for a fresh PIN. |
| The host is on but a service or an action is refused | By design, it is not declared in the configuration file. The message says what is missing. |
| `The configuration of this host cannot be read` | The file has a mistake and is refused as a whole. Run `dym daemon --check-config --config …` to see which. |
| A command works in your terminal and fails as an action | It runs without a shell, as the daemon's user, with a small environment. Use absolute paths and put anything that needs a shell inside a script. See [the configuration](daemon-configuration.md#actions). |

## Removing it

Stop and disable the service, delete the state file, and in Dymmer delete the host or change how it is reached (SSH or
disabled). Deleting the host or changing its way of reaching makes Dymmer refuse the old secret.

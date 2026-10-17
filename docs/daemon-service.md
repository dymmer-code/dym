# Running `dym daemon` as a service

`dym` is **one binary**. There is no installer and no package, and it does not create users or services by itself:
`dym daemon` runs as whoever starts it, and everything it does for Dymmer is done as that user. This page shows how to
choose that user and how to start the daemon at boot and restart it if it stops: with systemd (as the system or as a
normal user without root), on FreeBSD, and with plain cron when nothing else is available.

First read [`dym daemon`](daemon.md) for what it does and [the configuration file](daemon-configuration.md) for what you
allow it to do.

## Which user to use

**Use a specific, unprivileged user. Do not run it as root.**

The daemon can only do what its user can do. Dymmer can only ask for things that are on the list in the configuration
file, but those things are executed with the rights of the daemon's user. A mistake in the file, or a compromise on
the Dymmer side, is limited by the user you chose. Running as root would turn it into root access.

Two good choices:

1. **A dedicated user (`dym`)** for hosts where Dymmer manages system services. It has no login, owns nothing but its
   state, and gets root rights only through a few exact `sudo` lines. This is the default recommendation.
2. **The user that owns the application**, when the actions are about that user's own programs (Docker Compose of
   their own, a script in their `~/bin`, their own logs). No `sudo` is needed, because the user already may do those
   things. It is also the only way to run it **without root** (see below).

Avoid:

- **root**, as said.
- **A user in the `docker` group, if you only want to expose a few things.** The `docker` group is equivalent to root:
  whoever can talk to the Docker socket can mount the whole disk in a container. Prefer rootless Docker for that user,
  or a `sudo` rule for the exact `docker` commands you want.
- **Giving the user general `sudo`.** Write one exact `sudoers` line for each command (see
  [`privilege`](daemon-configuration.md#privilege)), never `ALL`.

### Creating the dedicated user (Linux, as root)

```sh
useradd --system --create-home --home-dir /var/lib/dym --shell /usr/sbin/nologin dym
install -d -o root -g dym -m 0750 /etc/dym            # configuration: readable by the daemon, written by root
install -m 0755 dym /usr/local/bin/dym
sudo -u dym dym auth daemon --state /var/lib/dym/daemon.json    # pairing: type the PIN in Dymmer
```

State goes in `/var/lib/dym/daemon.json` (mode `0600`, owned by `dym`) and the configuration in `/etc/dym/daemon.yaml`
(`root:dym`, `0640`: the daemon reads it and cannot change it).

## Linux, with systemd, as a system service

Save as `/etc/systemd/system/dym-daemon.service`:

```ini
[Unit]
Description=Dymmer daemon (keeps this host connected to Dymmer)
Documentation=https://github.com/dymmer-code/dym
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=dym
Group=dym
ExecStart=/usr/local/bin/dym daemon --state /var/lib/dym/daemon.json --config /etc/dym/daemon.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload
systemctl enable --now dym-daemon
journalctl -u dym-daemon -f           # look for: connected to Dymmer
```

You can enable the service **before** pairing: `dym daemon` waits for the state file and looks for it every five
seconds.

### A hardened variant

When the daemon only reads and calls things that need no privileges (no `privilege` in the configuration), the service
can lose almost everything. Add to `[Service]`:

```ini
NoNewPrivileges=true
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
LockPersonality=true
RestrictRealtime=true
RestrictSUIDSGID=true
RestrictNamespaces=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
UMask=0077
```

This **does not work with `sudo`**: `sudo` needs to gain privileges and change user, which `NoNewPrivileges`, the empty
capability set and the filter on privileged calls forbid. If the configuration uses `privilege: [sudo, -n]`, drop
`NoNewPrivileges`, `CapabilityBoundingSet`, `AmbientCapabilities`, `RestrictSUIDSGID`, `RestrictNamespaces` and both
`SystemCallFilter` lines, and `ProtectSystem`/`ProtectHome`/`PrivateDevices` too if the actions write files or use
devices. The commands the daemon may run as root are then the ones in `sudoers`, and nothing else.
`ProtectHome=true` also hides `/home`: leave it out if actions or logs live there.

## Linux, as a normal user, without root

You can run everything from your own account, with no administrator at all. Nothing in the daemon needs root: it uses
your user, your home and your Docker (if you can already use it).

**1. Put the binary in your home** and pair it:

```sh
mkdir -p ~/.local/bin
install -m 0755 dym ~/.local/bin/dym          # from the downloaded and checked archive
~/.local/bin/dym auth daemon                   # pairs; state goes to ~/.config/dym/daemon.json
```

**2. Write what Dymmer may do** (optional) in `~/.config/dym/daemon.yaml`, and check it:

```sh
~/.local/bin/dym daemon --check-config
```

**3. Start it at login or boot.** Choose the first that works on your machine.

### a) systemd user service (best)

Save as `~/.config/systemd/user/dym-daemon.service`:

```ini
[Unit]
Description=Dymmer daemon (keeps this host connected to Dymmer)
After=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/bin/dym daemon
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now dym-daemon
journalctl --user -u dym-daemon -f
```

`%h` is your home. `ExecStart` needs no `--state` or `--config`: for a normal user both default to `~/.config/dym/`.

A user service stops when you log out unless **lingering** is on, which keeps your services running from boot:

```sh
loginctl enable-linger "$USER"                 # may ask for an administrator once, depending on the system
loginctl show-user "$USER" -p Linger           # Linger=yes
```

If your system does not let you enable it, ask an administrator to run that one command, or use option b.

With **rootless Docker** the user's services need `DOCKER_HOST`; add it to the unit:
`Environment=DOCKER_HOST=unix://%t/docker.sock` (`%t` is `/run/user/<uid>`). The daemon passes `DOCKER_HOST`,
`XDG_RUNTIME_DIR` and a few more variables on to the commands it runs, and nothing else; see
[the environment of a command](daemon-configuration.md#the-environment-of-a-command).

### b) cron, when there is no systemd (or no lingering)

`crontab -e` is available to normal users. A small launcher that starts the daemon only if it is not running (`flock`
keeps two copies from starting), run every minute and at boot:

```sh
#!/bin/sh
# ~/.local/bin/dym-daemon-run.sh   (chmod +x)
exec flock -n "${XDG_RUNTIME_DIR:-/tmp}/dym-daemon.lock" "$HOME/.local/bin/dym" daemon
```

```cron
@reboot   nohup $HOME/.local/bin/dym-daemon-run.sh >> $HOME/.local/state/dym-daemon.log 2>&1 &
* * * * * nohup $HOME/.local/bin/dym-daemon-run.sh >> $HOME/.local/state/dym-daemon.log 2>&1 &
```

`flock -n` exits at once if the daemon already holds the lock, so the per-minute line only starts a new one after the
old one has died. Create the log directory first (`mkdir -p ~/.local/state`) and rotate the log if it matters.

### c) to try it, in a terminal

```sh
tmux new -s dym 'dym daemon'      # or: nohup dym daemon >> ~/dym.log 2>&1 &
```

## FreeBSD

As root, create the user and pair:

```sh
pw useradd dym -d /var/lib/dym -m -s /usr/sbin/nologin -c "Dymmer daemon"
install -d -o root -g dym -m 0750 /usr/local/etc/dym
install -m 0755 dym /usr/local/bin/dym
su -m dym -c 'dym auth daemon --state /var/lib/dym/daemon.json'
```

Save as `/usr/local/etc/rc.d/dym_daemon` (`chmod 0755`):

```sh
#!/bin/sh
# PROVIDE: dym_daemon
# REQUIRE: NETWORKING DAEMON
# KEYWORD: shutdown

. /etc/rc.subr

name="dym_daemon"
rcvar="dym_daemon_enable"
desc="Dymmer daemon (keeps this host connected to Dymmer)"

load_rc_config $name

: ${dym_daemon_enable:="NO"}
: ${dym_daemon_runas:="dym"}
: ${dym_daemon_state:="/var/lib/dym/daemon.json"}
: ${dym_daemon_config:="/usr/local/etc/dym/daemon.yaml"}

pidfile="/var/run/${name}.pid"
command="/usr/sbin/daemon"
# daemon(8) supervises (-r restarts, -R waits 5 s first) and sends the output to syslog. It stays root and chroot(8)
# changes the user of the process that runs: with `daemon -u` it drops its own privileges and then cannot restart it.
command_args="-r -R 5 -P ${pidfile} -S -T ${name} -l daemon -s info \
/usr/sbin/chroot -u ${dym_daemon_runas} -g ${dym_daemon_runas} / \
/usr/local/bin/dym daemon --state ${dym_daemon_state} --config ${dym_daemon_config}"

run_rc_command "$1"
```

```sh
sysrc dym_daemon_enable=YES
service dym_daemon start
tail -f /var/log/messages | grep dym_daemon
```

Two details that bite: **do not call the user variable `dym_daemon_user`**, because `rc.subr` takes `<name>_user` to
mean "run the whole service as this user" and then the service cannot write its pid file; and keep the
`/usr/sbin/chroot -u …  /` trick instead of `daemon -u`, for the reason in the comment. A normal user without root on
FreeBSD uses the cron method above.

## After it is running

- **Update:** replace the binary (the same path) and restart the service. The pairing and the configuration are not
  touched. The binary of the new version is announced in the [releases](https://github.com/dymmer-code/dym/releases).
- **Several hosts:** repeat the steps on each, with a PIN for each. If you manage them with Ansible, the whole of this
  page is a handful of tasks: create the user, copy the binary, copy the unit, `enable --now`, and pair once by hand
  (the PIN has to be typed by a person).
- **Stop for good:** disable the service, delete the state file, and delete the host in Dymmer or change how it is
  reached.

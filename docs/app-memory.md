# Application memory: collection and permissions

`dym app memory` reads memory **on the machine where the command runs**. It does
not contact Dymmer, create an app, start a container, or restart a service. The
paired daemon exposes the same collector as `app_memory_v1.sh`. Dymmer integration
is separate; older servers can continue using the other daemon operations.

```sh
dym app memory --manager systemctl --service dnsauth \
  --config /etc/dym/daemon.yaml
```

The result is a JSON document with `schema_version: 1`, `observed_at`, `status`,
`reason`, and `instances`. Every instance has a runtime identity, a label, status,
and readings with `metric`, `method`, `value` (integer bytes), nullable `capacity`,
and `accuracy`. Errors are states, not synthetic zeroes. A successfully produced
report exits zero even when it reports `permission_denied` or `stopped`; scripts
must inspect `status`. Invalid arguments or policy files exit nonzero.

## Two independent permissions

1. **Observation policy:** a `memory` entry in the configuration must authorize
   exactly the requested manager, service and, for Compose, directory.
2. **Operating-system access:** the account running dym must be able to read the
   kernel counters/process metadata or query Docker.

Granting `services` control permissions does not authorize memory observation,
and granting `memory` does not allow start, stop, restart or arbitrary commands.
The policy is re-read for every request; a policy change needs no daemon restart.
An in-flight read can finish after a policy edit; the receiver must revalidate its
own authorization before displaying or saving a response.

Use the same policy path as the daemon's `--config`. Defaults for the local CLI
are `/etc/dym/daemon.yaml` for root and the user's config directory for other users;
the CLI does not infer the running daemon's explicit path. A typical FreeBSD
service uses `/usr/local/etc/dym/daemon.yaml` explicitly.

Keep a system service's policy owned by root and readable by its daemon group:

```sh
chown root:dym /etc/dym/daemon.yaml
chmod 0640 /etc/dym/daemon.yaml
# Parent directories also need traversal permission for dym.
```

A personal SSH/CLI installation can use a user-owned policy. This describes the
account's own authorized reads; it is not a privilege boundary against that same
account. Do not put passwords, tokens, environment files or arbitrary commands in
memory entries. The collector never uses the global `privilege` command prefix.

## Linux with systemd

```yaml
memory:
  - manager: systemctl
    service: dnsauth
```

The account needs read access to unit properties, `/proc/<pid>/stat`,
`/proc/<pid>/cgroup`, boot identity, mount information and the cgroup's
`memory.current`/`memory.max`/`memory.stat` (v2), or corresponding v1 files.
These are normally readable without root. No service-control sudo rule,
Polkit permission to restart units, or write permission to cgroups is required.

Verify **as the actual daemon account**:

```sh
sudo -u dym systemctl show dnsauth \
  --property=MainPID,ControlGroup,InvocationID
sudo -u dym /opt/dym-daemon/dym app memory \
  --manager systemctl --service dnsauth --config /etc/dym/daemon.yaml
```

If procfs visibility or systemd sandboxing hides these files, the collector cannot
bypass it. Correct only the specific necessary visibility/read access; do not make
the daemon root or grant unrestricted sudo. This release does not automatically
change mount options, cgroup permissions, service accounting or service units.

## Docker and Compose (Linux, with or without systemd)

```yaml
memory:
  - manager: docker
    service: api
  - manager: docker_compose
    service: php
    path: /home/debian/docker/epp3-docker
    project: epp3-docker
```

`path` is a clean absolute remote directory: expand `$HOME` before declaring it.
`project` is optional on modern Compose with a working-directory label. It is
required for legacy containers without that label. It is an explicit binding
made by the host administrator, not a guess from the directory's basename.
Services with several replicas return individual instances, never a silently
summed total. The collector uses only `docker ps` and a field-limited
`docker inspect`; it does not invoke Compose, evaluate YAML or expose environment
variables. A conflicting available directory label is rejected.

The user needs access to the Docker endpoint and readable host `/proc` and cgroup
files for the returned container PIDs. Run dym in the host's process/cgroup
namespace, not an unrelated container. For rootless Docker, use the owner's
endpoint and environment; the returned host PIDs must still be visible.

**Access to a normal Docker socket, including membership in the `docker` group,
usually grants control of the host, not just observation.** Do not add the dym
user to that group or chmod the socket automatically. If an existing account
already has authorized Docker access (such as the `debian` SSH account on epp3),
the collector uses it unchanged. A separate restricted Docker endpoint/proxy may
expose only the necessary read APIs, but is an administrator-managed prerequisite,
not something this release installs. With no access, report `permission_denied`.

## Standalone Linux and FreeBSD rc.d / SysV

For a process running under a dedicated, non-root application UID:

```yaml
memory:
  - manager: service
    service: dnsauth
    uid: 1002
    executable: dnsauth
```

Use the actual local UID; `1002` is only the ns2 example. `executable` is the
basename reported by `ps`, not a shell command or regular expression. The
collector requires a unique root matching UID + executable, includes its visible
descendants, and checks process identities before/after sampling. Multiple roots
are `ambiguous`. Never reuse a shared service UID and common executable such as
`python` to distinguish unrelated apps. No match is `not_found`, not proof that a
service is stopped: OS process visibility can hide it.

This does **not** read a root-only pidfile or execute/source an rc.d/init.d script.
For ns2 the root supervisor `daemon` is excluded: dnsauth is selected directly
under its dedicated UID. There is no need to open `/var/run/dnsauth.pid`, enable
RACCT, grant sudo, add the account to wheel, or restart dnsauth.

Check visibility with separate `-o` arguments (portable across Linux/FreeBSD):

```sh
su -m dym -c 'ps -ax -o pid= -o ppid= -o uid= -o rss= -o lstart= -o comm='
su -m dym -c '/opt/dym-daemon/dym app memory --manager service --service dnsauth --config /usr/local/etc/dym/daemon.yaml'
```

FreeBSD also needs readable `sysctl kern.boottime`. Settings such as
`security.bsd.see_other_uids=0`, jail isolation or procfs restrictions on Linux may
hide the target. Do not disable host-wide isolation automatically. Choose a
suitable authorized collector account or leave this measurement unavailable.
FreeBSD process start times from `ps` have one-second precision; the combination
of boot time, PID, UID, executable and repeated membership checks reduces races
but cannot guarantee detecting same-second PID reuse. Reparented children are
not reliably attributable using a process tree. These readings are explicitly
an approximation, not complete service accounting.

For Linux without systemd, an administrator may additionally declare an exclusive
cgroup path (the path shown inside `/proc/<pid>/cgroup`):

```yaml
memory:
  - manager: init_d
    service: worker
    uid: 1005
    executable: worker
    cgroup: /apps/worker
```

The collector verifies membership matches that path. The administrator must
ensure it belongs exclusively to this app; do not declare a root, user-wide or
shared cgroup. Without this declaration the standalone selector reports RSS.

## Interpreting the readings

* `memory_accounted_bytes`: kernel cgroup accounting, **including file cache**.
  v1 can be approximate. Methods are `cgroup_v1` and `cgroup_v2`.
* `memory_file_cache_bytes`: optional separate cache counter. It is not subtracted
  from the main reading and has no capacity. It is not identical to every cache
  calculation used by `docker stats`.
* `memory_rss_bytes`: current resident memory summed over the visible process
  tree, method `process_rss`, accuracy `shared_pages_may_repeat`. Shared pages may
  be counted more than once. It is not peak RSS and cannot be compared directly
  to cgroup accounting.
* `cpu_usage_ns`: **cumulative** CPU time in nanoseconds. The consumer divides the
  difference between two observations of the same instance and method by the elapsed
  time; a value that goes down means a restart or processes that ended, and the
  interval is discarded, never shown as negative use. Methods: `cgroup_v2`
  (`cpu.stat`), `cgroup_v1` (`cpuacct.usage`) and `process_cputime` (the TIME of
  `ps`, accuracy `live_processes_only`: processes that already ended are not in it).
  The reading is left out when the host does not offer the counter; memory is still
  reported.

Every reading may carry a `capacity` and a `capacity_source` that says where it comes from:

* `limit`: a limit of the app itself (a finite cgroup or container limit; for CPU, the
  quota of the group). An ancestor's shared limit is never advertised as the app's own.
* `host`: no limit of its own, so the whole machine: the physical memory in bytes, or the
  logical CPUs in millicores (`4000` is four CPUs). It is only a ceiling to compare with,
  never a quota of the app.

Without `capacity` the machine could not be read. Swap is not included as a separate
measurement in v1. A changing instance/method must break the plotted segment.

At most 32 container instances and a 64 KiB report are allowed; process snapshots
and command output are bounded too. Oversized results are unavailable, never a
truncated total. Commands have timeouts and use argument arrays, never a shell.
The daemon's announcement of `app_memory_v1.sh` says the contract is supported;
the per-target response still determines whether that host can measure the app.

## Deployment

Install the release binary, retain the previous version for rollback, and restart
**only dym** to advertise its new operation. Apps do not need restarting. Add or
merge only the intended `memory` entries, preserving existing log/control policy.
Verify with the actual daemon/SSH user before enabling collection in Dymmer.
Configuration and agent version should also be recorded in your provisioning
system so that a future run does not overwrite them.

# AGENTS.md

Instructions for AI coding agents working in this repository (`dym`, a Go CLI for the Dymmer API — module `github.com/dymmer-code/dym`).

## Cutting a new release

When asked to "make a new version" / "cut a release", follow this exactly.

### 1. Tag format

Tags are **`vMAJOR.MINOR.PATCH`** — strict [semver](https://semver.org), always prefixed with a lowercase `v` (`v0.2.0`, never `0.2.0` or `V0.2.0`). Never use pre-release/build-metadata suffixes (`-rc1`, `+build`) unless explicitly asked.

### 2. Deciding which number to bump

Run `git log <last-tag>..HEAD --oneline` and classify every commit. Use the **highest** applicable bump if commits mix categories (one breaking change among ten fixes still means at least MINOR).

**While MAJOR is `0`** (initial development, current state): the CLI's own flags/commands are its public interface, so treat them like an API.
- **MINOR**: any new feature, new command, new flag, or a change that is backward-incompatible for a user (renamed/removed flag or command, changed default behavior — e.g. the `--format` → `--output` rename and the JSON→table default flip were MINOR bumps: `v0.1.0` → `v0.2.0`).
- **PATCH**: bug fixes, CI/release-infra changes, docs, dependency bumps, or internal refactors with no user-visible change.

**Once MAJOR >= `1`**: standard semver — MAJOR for backward-incompatible changes, MINOR for backward-compatible features, PATCH for backward-compatible fixes.

### 3. Writing the release notes

`gh release create --generate-notes` (used internally by `.github/workflows/release.yml`) produces almost nothing useful here — this repo pushes directly to `main` with no PRs/labels, so GitHub's auto-categorization (configured in `.github/release.yml`) has nothing to group. **Write the release body yourself**, don't rely on the auto-generated one alone:

1. Review `git log <last-tag>..HEAD --oneline` — commit subjects follow [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `ci:`, `chore:`, `docs:`).
2. Group them under the same category names already defined in `.github/release.yml` (`Features`, `Bug fixes`, `Documentation`, `Dependencies`, `Other changes`) — skip categories with nothing in them, skip pure-noise commits with no user-visible effect.
3. One bullet per meaningful commit, factual, no marketing language.
4. Always end with a full-changelog link matching the existing convention:
   `**Full Changelog**: https://github.com/dymmer-code/dym/compare/<prev-tag>...<new-tag>`

### 4. Steps

```sh
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

Wait for the `Release` workflow to finish:

```sh
gh run list --repo dymmer-code/dym --workflow=release.yml --limit 1
```

The workflow auto-creates the release via `gh release create ... --generate-notes`. Once it completes, overwrite the body with the hand-written notes from step 3:

```sh
gh release edit vX.Y.Z --repo dymmer-code/dym --notes "$(cat notes.md)"
```

Verify every platform artifact actually attached (goreleaser + `gh release upload` has previously failed silently mid-batch — see "Known pitfalls" below):

```sh
gh release view vX.Y.Z --repo dymmer-code/dym
```

Current build matrix (`.goreleaser.yml`): darwin, linux, windows, freebsd × amd64, arm64, minus windows/arm64 (unsupported combo) = **7 binaries** (windows as `.zip`, the rest as `.tar.gz`) **+ `checksums.txt`** = **8 assets total**. Update this expected count here if the build matrix in `.goreleaser.yml` ever changes.

### 5. Never retag a real release

Don't delete/recreate an already-published tag that anyone may have pulled. If a mistake is found after a real release, cut a new patch/minor version instead. (Early sessions did delete+recreate `v0.1.0` a few times while validating the release pipeline itself before it was a "real" release — that workflow-testing exception does not apply once a version has shipped for real.)

## Known pitfalls (already fixed once, don't reintroduce)

- **`gh release upload dist/*` breaks on goreleaser's own metadata files.** goreleaser always writes `dist/digests.txt` (Docker-digests output, empty since this project builds no Docker images) plus `dist/config.yaml`/`dist/artifacts.json`/`dist/metadata.json` (internal bookkeeping). Uploading a 0-byte file via `gh release upload` fails with `HTTP 400: Bad Content-Length` and aborts the whole batch — meaning **no real binaries get uploaded** if this glob is ever widened back to `dist/*`. The upload step in `.github/workflows/release.yml` must stay an explicit allowlist: `dist/*.tar.gz dist/*.zip dist/checksums.txt`.
- **FreeBSD builds intentionally use `CGO_ENABLED=0`.** `internal/credentials` (via `github.com/zalando/go-keyring`) has no real secret-store backend on FreeBSD without cgo, and enabling cgo wouldn't help anyway — the real deployment target (a headless FreeBSD host) has no D-Bus Secret Service session running. `DYMMER_TOKEN` is the intended auth path there; don't add a cgo cross-compilation toolchain to the release workflow to "fix" this.

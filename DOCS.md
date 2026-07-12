# lorebox internals

This document describes how lorebox works under the hood: the URL scheme, the
request pipeline, caching and refresh mechanics, disk accounting and
eviction, authentication, the admin API, and the client-side tooling. For a
general introduction and quick start, see [README.md](README.md).

Everything lives in one Go binary (`package main`). Rough map of the source:

| File | Contents |
|---|---|
| `main.go` | Server core: handler, config, fetch/refresh/evict logic, `main()` |
| `dumb.go` | Dumb-HTTP file/directory serving, path exclusion & cache rules |
| `api.go` | Admin API endpoints under `/-/` |
| `client.go` | Remote-control CLI, client config, credential helper |
| `pages.go` | Embedded static pages (4xx/5xx, robots.txt) and `/repos.txt` |
| `tls.go` | TLS keypair loading with SIGHUP hot-reload |
| `usage.go` | CLI usage text |
| `util.go` | Path helpers, disk size parsing |

## Command verbs

`lorebox <verb>` dispatches in `main()`:

- `serve` — run the server.
- `gen-token` — generate a token pair (see [Authentication](#authentication)).
- `register` — install lorebox as a git credential helper for every host in
  the client config.
- `credential get` — the credential helper protocol endpoint (invoked by git,
  not by hand).
- anything else — treated as a remote-control command and handled by the
  client (`client.go`).

## URL scheme and repository identity

A cached repository is identified by its URL path, which *is* the upstream
locator: a request for

```
/github.com/alice/project/info/refs?service=git-upload-pack
```

maps to the upstream URL `https:/` + `/github.com/alice/project` (i.e.
`https://github.com/alice/project`) and to the on-disk mirror at
`<root>/github.com/alice/project`. There is no database; the filesystem under
`root:` is the entire state. Repo directories are recognized by containing a
`HEAD` file.

## Request pipeline

`handler.ServeHTTP` (main.go) routes each request in order:

1. `GET /robots.txt` — always served (denies crawlers), before auth.
2. If `auth: all`, HTTP Basic credentials are required for everything else.
3. `GET /repos.txt` — plain-text sorted list of cached repos.
4. `GET /-/...` — admin API. Requires an `admin`-level token **and** the
   header `X-Gitbox-Api: On` (a CSRF guard: browsers won't add it).
5. `GET .../info/refs?service=git-upload-pack` — smart-HTTP ref
   advertisement. If the repo directory does not exist yet, this triggers a
   **pullthrough** fetch (auth required under `auth: new`), then the request
   is handed to `git-http-backend` over CGI.
6. `POST .../git-upload-pack` — smart-HTTP pack transfer, handed to
   `git-http-backend`.
7. Any `git-receive-pack` request or other `?service=` value — `400`.
   Gitbox is read-only.
8. Any other `GET` — dumb-HTTP static serving from the document root
   (`serveFS` in dumb.go): directories render as an HTML index, files are
   streamed. A dumb-protocol `GET .../info/refs` (no `service=` parameter)
   on a missing repo also triggers pullthrough.

Smart HTTP is served by git's own `git-http-backend`, located at startup via
`git --exec-path` and invoked through `net/http/cgi` with
`GIT_PROJECT_ROOT=<root>` and `GIT_HTTP_EXPORT_ALL=1`.

### Path safety and exclusions

`serveFS` cleans the request path and resolves symlinks (`expandPath`), then
rejects anything that escapes the document root. Within a cached repo,
`excludedPath` hides files that are private or unstable:

- the `<root>/.tmp` staging directory,
- `lorebox.access` (the access-time marker),
- `config`, `description`, `FETCH_HEAD`, and everything under `hooks/`.

These are also skipped in directory listings.

### HTTP caching headers

`cacheablePath` decides the `Cache-Control` header for dumb-served files:

- Objects and packfiles (`.../objects/...`, except `objects/info/`) are
  content-addressed and thus immutable: `max-age=31536000, immutable`.
- Everything else (`HEAD`, `info/refs`, listings, ...):` max-age=60`.

## Pullthrough fetch

`fetchRepo` (main.go) caches an upstream repo. Concurrent requests for the
same repo are collapsed into one fetch via `golang.org/x/sync/singleflight`,
so a popular new repo is cloned exactly once.

The sequence:

1. **Disk policy check** — `applyDiskUsagePolicy` (see below); `deny` aborts
   here if the budget is exceeded.
2. **Probe** — `git ls-remote --exit-code <upstream>` verifies the upstream
   exists before committing to a clone (bounded by the regular git timeout).
3. **Clone into staging** — `git clone --mirror` into a temp dir under
   `<root>/.tmp` (bounded by the clone timeout). Cloning into staging means
   a failed/partial clone never appears in the served tree; `.tmp` is wiped
   on every startup.
4. **Reconfigure** — `configureNewRepo` unsets `remote.origin.mirror` and
   replaces the fetch refspecs with
   `+refs/heads/*:refs/heads/*` and `+refs/tags/*:refs/tags/*`. A true
   mirror fetch would prune *all* non-upstream refs; the narrowed refspecs
   keep future `refs/lorebox/*` refs (and anything else outside heads/tags)
   safe during refreshes.
5. **`git update-server-info`** — regenerates `info/refs` and
   `objects/info/packs` so the dumb protocol works.
6. **Publish** — measure the directory size, `os.Rename` the staging dir to
   its final path (atomic on the same filesystem), record the access time,
   register the repo in the in-memory map, and start its refresher
   goroutine.

`apiFetchHttp` / the `fetch-http` verb run the same path with an `http://`
upstream, for HTTP-only remotes. Pullthrough via plain `git clone` always
uses `https`.

## In-memory state

`handler.repos` maps the absolute on-disk path to a `repoDescription`:

```go
type repoDescription struct {
    repo    string       // "/github.com/alice/project"
    size    int64        // cached dirSize, bytes
    lastErr time.Time    // last failed refresh (zero if none)
    cancelRefresher func()
}
```

It is guarded by an RWMutex (`reposLock`). On startup, `walkRepos` rebuilds
the map by scanning the document root (up to 10 levels deep, skipping
`.tmp`), treating any directory containing `HEAD` as a repo, and spawns a
refresher goroutine per repo. Nothing else is persisted; killing and
restarting the server loses only the `lastErr` timestamps.

## Background refresh

Each cached repo has its own `refresher` goroutine. The base interval is
`timeouts: refresh: default:` plus a random jitter in
`[jitter.min, jitter.max]`, recomputed after every successful refresh so
repos don't thunder-herd upstream.

A refresh (`refreshRepo`) does:

1. `git ls-remote --symref origin HEAD` and `git symbolic-ref HEAD ...` to
   track upstream default-branch changes (non-fatal if it fails).
2. `git fetch --prune origin` (clone timeout applies).
3. `git update-server-info`.
4. Re-apply the disk usage policy.

On failure the repo's `lastErr` is stamped and the interval **doubles**
(exponential backoff) up to `refresh: max:`; on success it resets to the
jittered default. A refresher exits when its repo is evicted (its context is
cancelled, and it also self-terminates if it finds its repo gone from the
map).

## Disk accounting and eviction

`disk: max:` (parsed by `parseDiskSize`: `G`/`M`/`K` suffixes or plain
bytes) caps the *sum of cached repo sizes* as tracked in the in-memory map —
sizes are measured by walking the repo directory at clone/startup time.
When the budget is exceeded, `disk: policy:` decides:

- **`deny`** — new pullthrough fetches fail (existing repos keep refreshing).
- **`warn`** — log and carry on.
- **`lru`** — evict least-recently-used repos until under budget.

### Access tracking and LRU

Every ref advertisement (`.../info/refs`) touches a `lorebox.access` file
inside the repo (`recordAccess`, an mtime bump). LRU sorts repos by that
mtime and deletes the stalest first, skipping pinned repos, until usage is
under the cap. If only pinned repos remain and the budget is still
exceeded, it logs and gives up. A repo whose access file is missing is
treated as just-accessed (safe default).

### Pinning

`pin`/`unpin` set or unset `lorebox.pinned = true` in the repo's git config.
Pinned repos are immune to both LRU and explicit `evict`.

### Eviction

`evictRepo` removes the directory, cancels the refresher, and drops the repo
from the map. Refusals: unknown repo, pinned repo.

## Authentication

Three server modes (`auth:`):

- `all` — Basic auth on every request.
- `new` — Basic auth only where lorebox does work on your behalf: triggering
  a pullthrough fetch. Already-cached content is public.
- `none` — no auth (dangerous on public addresses).

The admin API additionally always requires an `admin` token (when auth is
enabled) regardless of mode.

### Tokens

`lorebox gen-token` generates a random 32-byte token and 4-byte token id
(both base64url). The **server** stores only `sha256(token)`, also
base64url, in `lorebox.yml`:

```yaml
tokens:
  - id: "id-XXXX"
    hash: "..."
    level: fetch   # or: admin
```

The **client** uses `id:token` as HTTP Basic username:password. No salt is
used — the token is already uniformly random, so rainbow tables don't apply.
Levels: `fetch` may trigger pullthrough; `admin` may also use the `/-/` API.

## Admin API

All endpoints are `GET`, require an admin token plus the `X-Gitbox-Api: On`
header, and return `text/plain` (status 500 on failure, with the operation
log as the body):

| Endpoint | Action |
|---|---|
| `/-/status` | Version, uptime, total storage, request count |
| `/-/list` | Table of cached repos: size, last refresh error, pin state |
| `/-/effective-config` | The server's config as YAML, defaults included |
| `/-/fetch/<repo>` | Cache `<repo>` (https upstream) |
| `/-/fetch-http/<repo>` | Cache `<repo>` over plain http |
| `/-/refresh/<repo>` | Refresh one repo now |
| `/-/refresh-all` | Refresh every cached repo (synchronous, can be slow) |
| `/-/pin/<repo>` / `/-/unpin/<repo>` | Toggle eviction protection |
| `/-/evict/<repo>` | Delete a repo from the cache |

`<repo>` is the same path used in clone URLs, e.g.
`/-/pin/github.com/alice/project`.

The CLI verbs in `client.go` are thin wrappers over these endpoints; they
print the HTTP status and the body verbatim.

## Client configuration

`~/.config/lorebox/client.yml` (must be permission `0600`; overridable with
`-config`):

```yaml
default: box.example.net        # box used when -box is not given
tokens:
  "box.example.net": "id-XXXX:TOKEN"
```

- The remote-control client picks the token matching the target box and
  sends it as Basic auth over HTTPS (HTTP with `-insecure`).
- `lorebox register` writes git global config entries
  `credential.https://<host>.helper = !<path-to-lorebox> credential` (and the
  http variant) for every host in `tokens:`.
- `lorebox credential get` implements the git credential-helper protocol:
  it reads the `protocol`/`host` attributes from stdin and answers with
  `username`/`password` if the host is in `tokens:`, staying silent
  otherwise so other helpers can take over.

## Server configuration reference

All keys, with defaults (see the annotated [`lorebox.yml`](lorebox.yml) for
commentary):

| Key | Default | Meaning |
|---|---|---|
| `root` | `.` | Document root; where mirrors live. Use an absolute path |
| `listen` | `:8080` | Bind address (`host:port`) |
| `auth` | `new` | `all` / `new` / `none` |
| `timeouts: git: regular` | `5m` | Timeout for short git commands (ls-remote, config, ...) |
| `timeouts: git: clone` | `30m` | Timeout for clone and fetch |
| `timeouts: refresh: default` | `12h` | Base refresh interval |
| `timeouts: refresh: max` | `20d` | Backoff ceiling for failing repos |
| `timeouts: refresh: jitter: min/max` | `20m` / `70m` | Random jitter added to each interval |
| `disk: max` | `10G` | Disk budget (`G`/`M`/`K` suffix or bytes) |
| `disk: policy` | — (required) | `lru` / `deny` / `warn` |
| `tokens` | `[]` | List of `{id, hash, level}` |
| `https: certificate` / `key` | — | Enable TLS; both or neither |

Durations accept `d`, `h`, `m`, `s` suffixes (`d` is a lorebox extension on
top of Go's duration syntax). Config values are validated at startup
(jitter ordering, refresh ordering, auth mode, cert/key pairing, disk
policy); violations are fatal.

CLI flags `-listen`, `-root` override the config; `-insecure` skips TLS.

The HTTP server runs with a 10s read timeout, 10m write timeout (packs can
be large), 20s idle timeout, and 10KB header cap.

## TLS

When `https:` is configured, lorebox serves TLS itself using a
`keypairReloader` (tls.go): the cert/key pair is loaded at startup and
reloaded on `SIGHUP`, so certificate rotation (e.g. by certbot) needs no
restart. If the reload fails, the old certificate is kept. Alternatively run
lorebox plain (`-insecure` or no `https:` config) behind a TLS-terminating
reverse proxy.

`makecert.sh` generates a throwaway self-signed pair for local testing.

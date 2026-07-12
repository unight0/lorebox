# lorebox

A caching pull-through mirror for git repositories, in a single binary.

Point your `git clone` at your box instead of the upstream host, and lorebox
will transparently mirror the repository on first access, serve it to you over
git's HTTP protocols, and keep it fresh in the background. Think of it as a
personal read-only cache/archive of the repos you care about: it keeps working
when upstream is slow, rate-limited, or gone.

```
git clone https://github.com/alice/project        # normal
git clone https://box.example.net/github.com/alice/project   # via your lorebox
```

The URL path encodes the upstream: `/<host>/<path>` is fetched from
`https://<host>/<path>`. Anything already cached is served locally; anything
new is cloned on demand ("pullthrough") and cached.

The same binary is also the remote-control client and a git credential
helper.

## Features

- Pull-through caching: first fetch mirrors the upstream repo
  (`git clone --mirror`), subsequent fetches are served from disk via
  git's smart HTTP protocol (and the dumb protocol as a fallback).
- Background refresh: every cached repo is re-fetched periodically with
  configurable interval and jitter; failing repos back off exponentially.
- Disk budget: a maximum disk usage with a choice of `lru` (evict least
  recently used), `deny` (refuse new fetches), or `warn` policies.
- Pinning: pinned repos are never evicted.
- Token auth: SHA-256-hashed bearer tokens with `fetch` and `admin`
  levels; auth can be required for everything, only for new fetches, or
  disabled.
- Remote control: an authenticated HTTP API (`/-/...`) and a matching
  CLI: list, status, fetch, refresh, pin/unpin, evict.
- HTTPS: built-in TLS with certificate hot-reload on `SIGHUP`, or run it
  plain behind your existing reverse proxy.
- Browsable: cached repos are listed at `/repos.txt` and browsable as a
  plain directory index in any web browser.

## Building

Requires Go and a `git` binary on the server's `PATH` (lorebox shells out to
git and uses `git-http-backend` for smart HTTP).

```sh
go build
```

## Quick start (server)

1. Create a config. Start from the annotated [`lorebox.yml`](lorebox.yml) in
   this repo; the important keys are `root:` (where mirrors are stored),
   `listen:`, `auth:`, and `disk:`.

2. Generate a token and paste the server half into `lorebox.yml`:

   ```sh
   ./lorebox gen-token
   ```

3. (Optional) Enable HTTPS by pointing `https: certificate:`/`key:` at your
   cert. For local testing, `./makecert.sh` generates a self-signed pair.

4. Run:

   ```sh
   ./lorebox serve -config lorebox.yml
   ```

   `-listen` and `-root` override the config; `-insecure` disables TLS even
   if certificates are configured.

## Quick start (client)

You can just clone with the token embedded in the URL:

```sh
git clone https://id-XXXX:TOKEN@box.example.net/github.com/alice/project
```

Or set up lorebox as a git credential helper so plain URLs work:

1. Put the private half of the token into `~/.config/lorebox/client.yml`
   (must be chmod `0600`):

   ```yaml
   default: box.example.net
   tokens:
     "box.example.net": "id-XXXX:TOKEN"
   ```

2. Register lorebox with git:

   ```sh
   ./lorebox register
   git clone https://box.example.net/github.com/alice/project
   ```

## Remote control

With an `admin`-level token configured in `client.yml`, the same binary
drives the box remotely:

```sh
lorebox status                          # uptime, storage, request count
lorebox list                            # cached repos, sizes, pin state
lorebox fetch /github.com/alice/project    # cache a repo ahead of time
lorebox refresh /github.com/alice/project  # refresh one repo now
lorebox refresh-all                     # refresh everything
lorebox pin /github.com/alice/project   # protect from eviction
lorebox unpin /github.com/alice/project
lorebox evict /github.com/alice/project # delete from cache
lorebox effective-config                # dump the server's live config
```

`-box`, `-auth`, and `-insecure` override the config per invocation. See
`lorebox` with no arguments for full usage.

## Notes

- Gitbox is _read-only_: pushes (`git-receive-pack`) are rejected. It
  mirrors upstreams; it does not host original repositories.
- Upstreams are fetched over HTTPS. `fetch-http` exists for HTTP-only
  upstreams but is insecure: a man-in-the-middle can feed you arbitrary
  code, which you then cache.
- `auth: none` exposes fetch-anything-through-your-box to the internet;
  don't use it on a public address.

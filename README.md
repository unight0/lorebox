# lorebox

![lorebox demonstration](image1.png)

A single-binary git server that is two things at once: a caching pull-through
mirror for upstream repositories, and optionally a lightweight host for your own
repositories.

Lorebox is designed to be easy to use and set up. The easiest way to set it up
would be using docker-compose. Check `compose/` to see an example setup.
If you do not want to use docker, then you only need the lorebox binary, one
config file, one directory for storing repos, and a service description (for
systemd/runit/s6/etc). The defaults are sane

As a mirror, point your `git clone` at your box instead of the upstream host and
lorebox transparently mirrors the repo on first access, serves it over git's
HTTP protocols, and keeps it fresh in the background. It is a personal cache/archive
that keeps working even if upstream is slow or unavailable.

```sh
git clone https://github.com/alice/project                    # normal
git clone https://box.example.net/github.com/alice/project    # via your lorebox
```

The URL path encodes the upstream: `/<host>/<path>` is fetched from
`https://<host>/<path>`. Anything already cached is served locally; anything new
is cloned on demand ("pullthrough") and cached.

As a host, you can push your own repos into the `/~/<user>/...` namespace
(opt-in, off by default):

```sh
git push  https://box.example.net/~/alice/project   # create + push your own repo
git clone https://box.example.net/~/alice/project
```

The same binary is also the remote-control client and a git credential helper.

## Features

- Pull-through caching: first fetch mirrors the upstream repo
  (`git clone --mirror`); subsequent fetches are served from disk via git's smart
  HTTP protocol (with the dumb protocol as a fallback).
- Self-hosting: host your own repos under `/~/<user>/...` with push-to-create.
  Opt-in via `push: true`; without it lorebox is a pure read-only mirror.
- Private repos: self-hosted repos can be hidden — unlisted and clonable only
  by their owner (or an admin). New self-hosted repos are private by default.
- Background refresh: every mirrored repo is re-fetched periodically with a
  configurable interval and jitter; failing repos back off exponentially.
  (Self-hosted repos are never auto-refreshed.)
- Disk budget: a maximum disk usage with a choice of `lru` (evict least
  recently used), `deny` (refuse new fetches), or `warn`. Self-hosted repos are
  never evicted by the budget.
- Pinning: pinned mirrors are never evicted.
- Token auth: SHA-256-hashed tokens with `fetch`, `push`, and `admin` levels;
  auth can be required for everything, only for new/write actions, or disabled.
- Remote control: an authenticated admin API (`/-/...`) and a self-service
  API for repo owners (`/+/...`), each with matching CLI verbs.
- HTTPS: built-in TLS with certificate hot-reload on `SIGHUP`, or run it plain
  behind your existing reverse proxy.
- Browsable: all cached & unhidden (public) repos are listed at `/repos.txt` and
  browsable as a plain directory index in any web browser.

## Building & requirements

Building requries Go; Running requires a `git` binary in the server's `PATH`
(lorebox shells out to git and uses `git-http-backend` for smart HTTP).

```sh
go build
```
or
```sh
go install
```

### Alternatively, use docker

You can find the `Dockerfile` in the root source directory. It builds an image
that has its config located in /lorebox.yml, and it stores its data (cached &
hosted repos) in /lorebox.

There's also the `compose/` directory that contains the docker-compose setup for
easy start. It puts lorebox behind Caddy. You should edit `compose/Caddyfile` and
`compose/lorebox.yml` to tailor it to your preferences before running the image.

## Quick start (server)

1. Create a config. Start from the annotated [`lorebox.yml`](lorebox.yml) in this
   repo; the important keys are `root:` (where repos are stored), `listen:`,
   `auth:`, `disk:`, and `push:` (enable self-hosting).

2. Generate a token and paste the server half into `lorebox.yml`. The token id
   doubles as the username for self-hosting, so pick it accordingly:

   ```sh
   lorebox gen-token alice
   ```

   Choose a `level:` for it: either `fetch`, `push`, or `admin` (see
   [Auth & tokens](#auth--tokens)).

3. (Optional) Enable HTTPS by pointing `https: certificate:`/`key:` at your cert.
   For local testing, `./makecert.sh` generates a self-signed pair. Send the
   process `SIGHUP` to reload a renewed certificate without downtime. You can
   keep the box in HTTP-only mode if you want to proxy through your nginx/apache/etc.
   web server.

4. Run:

   ```sh
   lorebox serve -config lorebox.yml
   ```

   `-listen` and `-root` override the config; `-insecure` disables TLS even if
   certificates are configured.

## Quick start (client)

You can just clone with the token embedded in the URL:

```sh
git clone https://alice:TOKEN@box.example.net/github.com/alice/project
```

Or set up lorebox as a git credential helper so plain URLs work:

1. Put the private half of the token into `~/.config/lorebox/client.yml` (must be
   chmod `0600`):

   ```yaml
   default: box.example.net
   tokens:
     "box.example.net": "alice:TOKEN"
   ```

2. Register lorebox with git:

   ```sh
   lorebox register
   ```

3. You are good to go!
   ```sh
   git clone https://box.example.net/github.com/alice/project
   ```

Please notice that you have to run `lorebox register` each time you add a new
host to your client config, since it registers itself as an authenticator on a
per-host basis, not globally.

## Self-hosting your own repos

With `push: true` on the server, lorebox also hosts your own repos under
`/~/<user>/...`. Ownership is by token id: a `push`-level token with id `alice`
may create and push repos under `/~/alice/...`, but a token with `admin`-level
permissions may act on anyone's repo.

Pushing to a path that doesn't exist yet creates the repo:

```sh
git remote add box https://box.example.net/~/alice/project
git push box main                                    # creates it on first push
git clone https://box.example.net/~/alice/project    # clone it back
```

New self-hosted repos are private (hidden) by default: they are not listed in
`/repos.txt` or the directory index, and only the owner (or an admin) can clone
them. Publish one with `unhide`.

Manage them with the CLI (using the same push/admin token from your `client.yml`):

```sh
lorebox create alice/project   # create an empty repo (also happens on first push)
lorebox hide   alice/project   # make private (unlisted, owner-only)
lorebox unhide alice/project   # make public and listed
lorebox delete alice/project   # delete it
```

`create`, `delete`, and pushing require `push: true` on the server; `hide` and
`unhide` work regardless, since users should be able to hide/unhide their repos even
after destructive (constructive) actions were disabled. Notice that if `push`
was never set to `true` in the first place, `hide` and `unhide` don't have
effect. Self-hosted repos are treated as original content. They are never
background-refreshed and never evicted by the disk budget (an admin can still remove
one with `delete`).

## Remote control

With an `admin`-level token configured in `client.yml`, the same binary drives
the box remotely over the `/-/...` API:

```sh
lorebox status                             # uptime, storage, request count, etc.
lorebox list                               # repos, sizes, pin/hidden state
lorebox fetch /github.com/alice/project    # cache a repo ahead of time
lorebox refresh /github.com/alice/project  # refresh one repo now
lorebox refresh-all                        # refresh every mirror
lorebox pin /github.com/alice/project      # protect from eviction
lorebox unpin /github.com/alice/project
lorebox evict /github.com/alice/project    # delete from cache
lorebox effective-config                   # dump the server's live config
```

`-box`, `-auth`, and `-insecure` override the config per invocation. Run
`lorebox` with no arguments for full usage, or `lorebox synonyms` for the short
aliases.

## Auth & tokens

Tokens are SHA-256-hashed; the server stores only the hash. There are three
levels, each a superset of the one before:

- `fetch` — clone cached repos and trigger new mirror fetches.
- `push` — also create, push, delete, hide, and unhide self-hosted repos you
  own (`/~/<your-id>/...`) (via the `/+/` self-hosted repo remote-control API).
- `admin` — also use the `/-/` remote-control API, and act on any user's
  self-hosted repos.

The `auth:` config key controls _when_ a token is required:

- `all` — every request needs a token.
- `new` — serving already-cached repos is open; fetching an uncached repo and
  all self-hosted/admin actions need a token.
- `none` — no auth. This also leaves the admin API open, so never use it on a
  public address.

## Notes

- Mirrored upstreams are read-only pull-through caches; only repos in the
  `/~/...` self-hosted namespace accept pushes, and only when `push: true`.
- Upstreams are fetched over HTTPS. `fetch-http` exists for HTTP-only upstreams
  but is insecure: a man-in-the-middle can feed you arbitrary code, which you
  then cache.

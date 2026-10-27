<h1 align="center">webhookgo</h1>

<p align="center">
  Inspect webhooks and HTTP requests in real time.<br>
  <a href="https://webhookgo.dev"><strong>webhookgo.dev</strong></a>
</p>

<p align="center">
  <a href="https://github.com/dbaio/webhookgo/stargazers"><img alt="GitHub stars" src="https://img.shields.io/github/stars/dbaio/webhookgo?style=flat&logo=github"></a>
  <a href="https://github.com/dbaio/webhookgo/forks"><img alt="GitHub forks" src="https://img.shields.io/github/forks/dbaio/webhookgo?style=flat&logo=github"></a>
  <a href="https://github.com/dbaio/webhookgo/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/dbaio/webhookgo/actions/workflows/ci.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/dbaio/webhookgo"></a>
  <a href="LICENSE"><img alt="License: BSD-2-Clause" src="https://img.shields.io/badge/license-BSD--2--Clause-blue"></a>
</p>

<p align="center">
  <a href="https://github.com/dbaio/webhookgo">⭐ Star</a> ·
  <a href="https://github.com/dbaio/webhookgo/fork">Fork</a> ·
  <a href="https://github.com/dbaio/webhookgo/issues/new">Report an issue</a> ·
  <a href="#hosting">Self-host</a>
</p>

<p align="center">
  <img src="docs/screenshot.png" alt="webhookgo: a list of captured requests and a JSON webhook body, with its signature marked valid" width="900">
</p>

Get a unique URL, point any service at it, and watch every request arrive
instantly: headers, query string, form fields, files and body, pretty-printed.

A modern take on [webhook.site](https://github.com/fredsted/webhook.site): a
single Go binary with the UI embedded, Redis for storage, and live updates over
Server-Sent Events. No PHP, no Node at runtime, no socket.io server.

## Contents

- [Quick start](#quick-start)
- [Features](#features)
- [Configuration](#configuration)
- [Hosting](#hosting): Cloudflare Tunnel, ngrok, Caddy, nginx
- [Abuse handling](#abuse-handling)
- [Signature verification](#signature-verification)
- [API](#api)
- [End-to-end tests](#end-to-end-tests)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Quick start

```sh
docker compose up -d
open http://localhost:8080
```

This builds the image from source. With a `.env` copied from
[`.env.example`](.env.example), it pulls the published `latest` image instead.
To put it online with HTTPS, see
[Hosting](#hosting). You can use a tunnel (Cloudflare, ngrok) or a reverse
proxy (Caddy, nginx), each switched on from `.env`.

### Without cloning

You can run the published image from GitHub Container Registry (amd64 and
arm64) with just the Compose file:

```sh
mkdir webhookgo && cd webhookgo
curl -fsSLO https://raw.githubusercontent.com/dbaio/webhookgo/main/docker-compose.yml
printf 'IMAGE=ghcr.io/dbaio/webhookgo:latest\nPULL_POLICY=always\n' > .env
docker compose up -d
```

`latest` is the newest build of `main`, and `PULL_POLICY=always` gets it on
every `docker compose up -d`. To stay on a release, use a version tag such as
`ghcr.io/dbaio/webhookgo:1`.

## Features

- **Unique URLs** that accept any method and any path: `/<uuid>`, `/<uuid>/anything/here?x=1`.
- **Live**: requests appear the moment they arrive (SSE + Redis pub/sub), across tabs and app replicas. Where something in between holds back streaming (e.g. Cloudflare Quick Tunnels), the UI notices and polls instead.
- **Custom responses** per URL: status code, body, `Content-Type`, extra headers, delay (0–30 s) and CORS (with real preflight handling).
- **Signature verification** for GitHub, Stripe, Standard Webhooks/Svix, Shopify, Slack, Malga or any HMAC header, with an option to answer 401 when it fails.
- **Status from the path**: `/<uuid>/404` answers 404, `/<uuid>/503/retry` answers 503.
- **Body viewer**: collapsible JSON tree (lossless — 64-bit IDs and key order survive), highlighted JSON/XML, form tables, hex dump for binary, image preview; download the raw body.
- **Copy as** cURL, HTTPie, JavaScript `fetch`, Python `requests`, Go `net/http` or raw HTTP.
- **Forwarding** from the browser to any target — including `http://localhost` on your machine — on demand or automatically for each new request.
- **Search** across bodies, headers, query strings and IPs; filter by method.
- **Wait for a webhook** from scripts and end-to-end tests: one API call returns as soon as a matching request arrives.
- **Responsive UI** with light/dark themes, keyboard shortcuts (`?`), desktop notifications, unread markers and a history of your URLs.
- **Abuse handling**: an `/abuse` page where anyone can look up and delete reported URLs, with records kept for investigations.
- **Safe defaults**: per-IP rate limits, body and per-URL storage caps, custom responses sandboxed with CSP, strict CSP on the app, distroless non-root read-only container (~12 MB image).

## Configuration

Everything is set through environment variables. With Docker Compose, put
overrides in a `.env` file next to `docker-compose.yml` (see [`.env.example`](.env.example)).

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | Host port (Compose only); `127.0.0.1:8080` to accept local connections only. |
| `IMAGE` / `PULL_POLICY` | build from source | Compose only. Runs a published image instead, e.g. `ghcr.io/dbaio/webhookgo:latest` with `always`. |
| `PUBLIC_URL` | browser origin | Base URL shown for webhook URLs, e.g. `https://hooks.example.com`. |
| `APP_NAME` | `webhookgo` | Name shown in the UI. |
| `MAX_REQUESTS` | `500` | Requests kept per URL; the oldest are dropped as new ones arrive. |
| `MAX_URL_STORAGE` | `64MB` | Bytes kept per URL; the oldest requests are dropped beyond it (`0` = unlimited). |
| `TOKEN_TTL` | `7d` | A URL and its requests expire after this long without use (`72h`, `30d`…). |
| `MAX_BODY_SIZE` | `2MB` | Stored bytes per body; larger bodies are truncated (the real size is still reported). |
| `MAX_DELAY` | `30` | Maximum response delay, in seconds. |
| `RATE_LIMIT` | `600` | Captured requests per minute per client IP (`0` = unlimited). |
| `TOKEN_RATE_LIMIT` | `30` | New URLs per minute per client IP (`0` = unlimited). |
| `API_RATE_LIMIT` | `1200` | API calls (`/token/…`) per minute per client IP (`0` = unlimited). |
| `AUDIT_RETENTION` | `180d` | How long the record of a URL is kept after it expires or is deleted (`0` = none). See [Abuse handling](#abuse-handling). |
| `ABUSE_CONTACT` | none | Email or URL shown on the `/abuse` page. |
| `TRUST_PROXY` | `false` | Honour `X-Forwarded-*` from your reverse proxies: `true` for one, or how many. The client IP is read from the right of `X-Forwarded-For`, so it can't be spoofed. Behind Cloudflare's proxy, see [Behind Cloudflare](#behind-cloudflares-proxy). |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection (`rediss://` for TLS). |
| `LISTEN_ADDR` | `:8080` | Address the server listens on. |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`…`error`; `text` or `json`. |
| `REDIS_MAXMEMORY` | `512mb` | Redis memory cap (Compose only). |

## Hosting

All of this is optional. A plain `docker compose up -d` runs only the app and
Redis, with no third-party service involved.

Every option below is a Compose override in [`deploy/`](deploy). You switch it
on with `COMPOSE_FILE` in `.env`, so the command stays the same:
`docker compose up -d`. Each override also sets `PUBLIC_URL` and `TRUST_PROXY`
for you.

| Option | You need | Open ports | Notes |
| --- | --- | --- | --- |
| [Cloudflare Quick Tunnel](#cloudflare-quick-tunnel-no-account) | nothing | none | Random `*.trycloudflare.com` URL that changes on restart. For trying things out. |
| [Cloudflare Tunnel](#cloudflare-tunnel-your-domain) | Cloudflare account, domain on Cloudflare | none | Stable URL on your domain. |
| [ngrok](#ngrok) | free ngrok account | none | Stable `*.ngrok-free.app` URL. |
| [Caddy](#caddy-automatic-https) | server, domain | 80, 443 | Automatic Let's Encrypt certificate. |
| [nginx](#nginx) | server, domain, certbot | 80, 443 | Most faithful: passes every header. |

The commands below assume Linux or macOS. On Windows, separate the files in
`COMPOSE_FILE` with `;` instead of `:`.

### Cloudflare Quick Tunnel (no account)

```sh
# .env
COMPOSE_FILE=docker-compose.yml:deploy/cloudflare/quick.yml
```

```sh
docker compose up -d
make tunnel-url   # or: docker compose logs cloudflared | grep trycloudflare.com
```

The app is still available on `http://localhost:8080`. Quick Tunnels do not
stream Server-Sent Events, so the UI polls every 3 s instead. They are also
limited to 200 requests in flight.

### Cloudflare Tunnel (your domain)

1. In the Cloudflare dashboard, go to **Zero Trust → Networks → Tunnels**, create
   a tunnel of type *Cloudflared* and copy its token.
2. Add a public hostname to the tunnel, e.g. `hooks.example.com`, with service
   `HTTP` and URL `app:8080`.
3. Add this to `.env`, then run `docker compose up -d`:

```sh
# .env
COMPOSE_FILE=docker-compose.yml:deploy/cloudflare/compose.yml
CLOUDFLARE_TUNNEL_TOKEN=eyJhIjoi...
DOMAIN=hooks.example.com
```

What changes behind Cloudflare: captured requests also show Cloudflare's
headers (`cf-connecting-ip`, `cf-ray`, …), and only standard HTTP methods get
through. A method such as `PURGE` is answered `400` by Cloudflare itself.

### ngrok

1. Sign up at [ngrok.com](https://ngrok.com) and copy your authtoken. Then copy
   your free static domain from **Domains** in the dashboard.
2. Add this to `.env`, then run `docker compose up -d`:

```sh
# .env
COMPOSE_FILE=docker-compose.yml:deploy/ngrok/compose.yml
NGROK_AUTHTOKEN=2abc...
NGROK_DOMAIN=example-name.ngrok-free.app
```

The app is still available on `http://localhost:8080`. On free accounts, ngrok
shows browsers a warning page the first time. Webhook senders are not
affected.

### Caddy (automatic HTTPS)

Point the domain's DNS at the server and open ports 80 and 443. Then add this
to `.env` and run `docker compose up -d`:

```sh
# .env
COMPOSE_FILE=docker-compose.yml:deploy/caddy/compose.yml
DOMAIN=hooks.example.com
```

Caddy gets a Let's Encrypt certificate on first start and renews it by itself.
Certificates are kept in the `caddy-data` volume. The app port is no longer
published, so the only way in is through Caddy.

Webhook URLs also answer over plain HTTP, like on webhook.site, for senders that
cannot do TLS. The UI and API redirect to HTTPS. The configuration is in
[`deploy/caddy/Caddyfile`](deploy/caddy/Caddyfile).

> **Caddy 2.11.4+ drops request headers whose names contain `_` or `.`**
> (a fix for PHP/CGI backends, GHSA-f59h-q822-g45g), so they never show up in
> captured requests. You can let specific names through in the Caddyfile. When
> every header matters, use nginx.

### nginx

[`deploy/nginx/webhookgo.conf`](deploy/nginx/webhookgo.conf) is a site for nginx
installed on the host, with the certificate from certbot:

```sh
# .env: publish the app on localhost only; nginx is the way in
PORT=127.0.0.1:8080
PUBLIC_URL=https://hooks.example.com
TRUST_PROXY=1
```

```sh
docker compose up -d
sudo certbot certonly --nginx -d hooks.example.com
sudo cp deploy/nginx/webhookgo.conf /etc/nginx/conf.d/   # then replace hooks.example.com in it
sudo nginx -t && sudo systemctl reload nginx
```

The site does the same as the Caddy setup: HTTP/2, webhooks also over plain
HTTP, and the UI and API redirect to HTTPS. On top of that it keeps:

- headers with underscores, which nginx drops by default
- long headers
- bodies up to 32 MB
- responses byte for byte, with no gzip added

It needs nginx 1.25.1 or later. The file has a note for older versions.

### Behind Cloudflare's proxy

When the domain's DNS record is proxied by Cloudflare (orange cloud) in front
of Caddy or nginx, every request reaches the proxy from a Cloudflare address.
To show the real client, let the proxy take it from `CF-Connecting-IP`, but
only on connections from [Cloudflare's ranges](https://www.cloudflare.com/ips),
so a request sent straight to your server can't forge it. `TRUST_PROXY` stays
`1`.

- **Caddy**: uncomment `trusted_proxies` and `client_ip_headers` in
  [`deploy/caddy/Caddyfile`](deploy/caddy/Caddyfile).
- **nginx**: write the ranges to a file with the command at the top of
  [`deploy/nginx/webhookgo.conf`](deploy/nginx/webhookgo.conf) and uncomment
  its `include` line.

A Cloudflare Tunnel needs none of this: there the app reads the client from
`X-Forwarded-For`, which Cloudflare fills in.

### Any other reverse proxy

- **Proxy settings**: set `PUBLIC_URL` and `TRUST_PROXY` (the number of proxies in front of the app).
- **App port**: keep it private. Bind it with `PORT=127.0.0.1:8080`, or don't publish it at all. If it can be reached directly, a client can forge `X-Forwarded-For`.
- **Live stream**: don't buffer `text/event-stream` responses. The app already sends `X-Accel-Buffering: no`.
- **Request size**: allow request bodies of at least `MAX_BODY_SIZE`.
- **Timeouts**: use a read timeout longer than `MAX_DELAY`.

## Abuse handling

A public instance will be abused, like any webhook service: phishing kits post
the credentials they collect to a URL, or a URL serves the phishing page
itself. As on webhook.site, the people who report it can act on their own:
`/abuse` (*Report abuse* in the app's menu) takes URLs, IDs or a whole report,
shows when and from which IP each URL was created, how many requests it got and
what it answers, and deletes it. A deleted URL answers `410 Gone` from then on.
Set `ABUSE_CONTACT` to show where to report anything else.

Each URL also gets a small record that outlives it by `AUDIT_RETENTION` (180
days by default; `0` keeps none): when and from which IP and user agent it was
created, how many requests it got and when the first and last arrived, and when
and from where it was deleted. Captured requests are never part of it. To read
records:

```sh
docker compose exec app webhookgo audit https://hooks.example.com/<uuid>
docker compose exec -T app webhookgo audit < report.eml   # every ID in the text
```

Records live in Redis, next to the URLs:

- They survive restarts and `docker compose down`: Redis runs with `appendonly yes` on the `redis-data` volume, and a crash loses about a second of writes at most. `docker compose down -v` deletes the volume, and everything with it.
- Each takes a few hundred bytes of `REDIS_MAXMEMORY`. When Redis runs out, it evicts the least recently used keys, which can include old records. Give it room (watch `evicted_keys` in `redis-cli INFO stats`), and keep the output of `webhookgo audit` with each report you act on.
- IP addresses are personal data under laws such as the GDPR and Brazil's LGPD: say in your terms what you keep and for how long.

To block an abuser's IP address, use your firewall, or Cloudflare's IP Access
Rules when it is in front.

## Signature verification

A URL can check the signatures that providers put on their webhooks. Pick the
provider in **Response settings → Signature verification** and paste the
secret. Each request is then marked with one of these statuses:

| Status | Meaning |
| --- | --- |
| `valid` | The signature matches. |
| `invalid` | The signature does not match, or the header is malformed. |
| `expired` | The signature matches, but its timestamp is further from now than the tolerance (5 minutes by default). |
| `missing` | The request has no signature headers. |
| `unverifiable` | The body is too large to check. |

| Scheme | Headers | What is signed |
| --- | --- | --- |
| `github` | `X-Hub-Signature-256` | HMAC-SHA256 of the body |
| `stripe` | `Stripe-Signature` | HMAC-SHA256 of `<t>.<body>` |
| `standard` (Standard Webhooks, Svix) | `webhook-id`, `webhook-timestamp`, `webhook-signature`, or the `svix-` ones | HMAC-SHA256 of `<id>.<timestamp>.<body>`, keyed with the `whsec_` secret |
| `shopify` | `X-Shopify-Hmac-Sha256` | HMAC-SHA256 of the body, in base64 |
| `slack` | `X-Slack-Signature`, `X-Slack-Request-Timestamp` | HMAC-SHA256 of `v0:<timestamp>:<body>` |
| `malga` ([Malga webhooks v1.1](https://docs.malga.io/documentations/webhooks/webhook1-1)) | `X-Plug-Signature`, `X-Plug-Date` | Ed25519 signature of `<X-Plug-Date>`, a line break and the body, checked with the webhook's public key |
| `hmac` | a header you choose | HMAC-SHA1, SHA-256 or SHA-512 of the body, in hex or base64, after an optional prefix |

HMAC signatures are checked against the whole body, even the part past
`MAX_BODY_SIZE` that is not stored. Ed25519 needs the stored body, so an
`malga` request larger than `MAX_BODY_SIZE` is `unverifiable`.

With **Answer 401** on, requests that are not `valid` get `401` instead of the
URL's response. They are still recorded.

In the API, `POST /token` and `PATCH /token/<token>` take the same settings in
`signature`:

- The secret is never returned, and leaving it out keeps the saved one.
- Changing `scheme` needs a new secret.
- `null` turns verification off.

```sh
curl -s -X PATCH localhost:8080/token/$TOKEN -H 'Content-Type: application/json' \
  -d '{"signature": {"scheme": "stripe", "secret": "whsec_...", "tolerance": 300, "reject": false}}'

# In a test: the latest request must carry a valid signature
curl -s localhost:8080/token/$TOKEN/request/latest | jq -e '.signature.status == "valid"'
```

## API

Routes mirror webhook.site's open-source API. Differences: `query` and `form` are
ordered `[{name, value}]` lists (repeated names are kept), header names are lower-case,
an unknown URL answers `404`, and a deleted one `410`.

| Method | Path | |
| --- | --- | --- |
| `ANY` | `/<token>[/<status>][/<path>]` | Capture a request. |
| `POST` | `/token` | Create a URL. JSON or form body: `default_status`, `default_content`, `default_content_type`, `timeout`, `cors`, `description`, `response_headers` (`[{name, value}]`), `signature` (JSON only, see [Signature verification](#signature-verification)). |
| `GET` / `PUT` / `PATCH` / `DELETE` | `/token/<token>` | Read, update (partial), delete a URL. |
| `PUT` | `/token/<token>/cors/toggle` | Toggle CORS. |
| `GET` | `/token/<token>/requests` | List requests: `page`, `per_page`, `sorting=newest\|oldest`, `q` (search), `method=GET,POST`, `summary=1`. |
| `GET` | `/token/<token>/request/latest` | Most recent request. |
| `GET` | `/token/<token>/wait` | Wait for a matching request: `since`, `timeout`, `match`, `q`, `method`. See [End-to-end tests](#end-to-end-tests). |
| `GET` / `DELETE` | `/token/<token>/request/<id>` | Read or delete a request. |
| `GET` | `/token/<token>/request/<id>/raw` | The body as received (`?download=1` to download). |
| `DELETE` | `/token/<token>/request` | Delete all requests. |
| `GET` | `/token/<token>/stream` | Live events (Server-Sent Events). |
| `GET` | `/healthz` | Health check (pings Redis). |

```sh
URL=$(curl -s -X POST localhost:8080/token -H 'Content-Type: application/json' \
  -d '{"default_status": 202, "default_content": "{\"ok\":true}", "default_content_type": "application/json"}' | jq -r .url)
curl -s -X POST "$URL/orders" -d 'id=42'
curl -s "localhost:8080/token/${URL##*/}/request/latest" | jq .
```

## End-to-end tests

A test can trigger something that sends a webhook, then wait for that webhook
to arrive. `GET /token/<token>/wait` answers with the oldest matching request
that arrived at or after `since`. It answers as soon as there is one, or with
`408` after `timeout` seconds.

| Parameter | Default | |
| --- | --- | --- |
| `since` | when the call starts | Unix time in seconds or milliseconds, or an RFC 3339 date. Pass the time you triggered the webhook, so a webhook that arrives before the wait starts still counts. `0` searches everything stored. |
| `timeout` | `30` | Seconds to wait, from `0` to `60`. `0` only checks what is stored. To wait longer, call again. |
| `match` | | `<field>=<value>`, an exact match. Repeat it to require several. Fields: `body.<path>` for JSON bodies (`body.data.id`, `body.items.0.sku`), `header.<name>`, `query.<name>`, `form.<name>` and `path`. |
| `q`, `method` | | Text search and methods, as in the request list. |

```sh
HOOKS=https://hooks.example.com
TOKEN=...   # a URL you created once and registered with the provider

T0=$(date +%s)
ID=$(curl -fsS -X POST https://api.example.com/orders -d '...' | jq -r .id)

curl -fsS -G "$HOOKS/token/$TOKEN/wait" \
  --data-urlencode "since=$T0" \
  --data-urlencode "match=body.data.id=$ID" \
  --data-urlencode "timeout=60" | jq .
```

With `-f`, curl fails on the `408`, so the script fails when the webhook is
late. The response is the full request, as in `/token/<token>/request/<id>`.
Its `created_at` gives the delivery delay.

How `match` compares values:

- JSON strings must be equal.
- Numbers compare as written in the body, so `body.amount=10` does not match `10.0`.
- `true`, `false` and `null` match those JSON values.
- Header values must be equal, but header names ignore case.

A URL expires after `TOKEN_TTL` without use. For a URL that stays registered
with a provider, raise it, for example `TOKEN_TTL=365d`.

## Development

Requirements: Go 1.26+, Node 22+, and Redis (or Docker).

```sh
make dev-redis    # terminal 1 — Redis on :6379
make dev-server   # terminal 2 — Go server on :8080
make dev-web      # terminal 3 — Vite with hot reload on :5173 (proxies to :8080)

make test         # go test -race ./... and svelte-check
make build        # bin/webhookgo with the UI embedded
```

### Releases

The CI workflow ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) builds
the image for amd64 and arm64. Once the tests pass, it publishes the image to
GitHub Container Registry:

| You push | Image tags |
| --- | --- |
| a commit to `main` | `latest` |
| a tag such as `v1.2.3` | `1.2.3`, `1.2`, `1` |

Push the tag on its own, after the commit it points to:

```sh
git push origin main
git tag v1.2.3 && git push origin v1.2.3
```

New packages start out private. After the first publish, go to the
repository's **Packages → webhookgo → Package settings → Change visibility**
and choose *Public*. You only need to do this once.

### Layout

```
cmd/webhookgo/       entry point (also `webhookgo healthcheck` and `webhookgo audit`)
internal/config/    environment configuration
internal/store/     Redis storage: atomic Lua scripts, hash-tagged keys, rate limiting
internal/hub/       one Redis pub/sub subscription fanned out to SSE clients
internal/signature/ signature checks for webhook providers
internal/server/    capture, JSON API, live stream, embedded static files
web/                Svelte 5 + TypeScript + Vite UI, embedded via go:embed
deploy/             Compose overrides and configs for tunnels and reverse proxies
```

### Storage model

Each URL owns five Redis keys sharing a hash tag (Cluster-friendly) and one
TTL that is refreshed on every use: `wh:{id}:token` (settings), `wh:{id}:index`
(sorted set of request IDs by time), `wh:{id}:sum` (small summaries for lists),
`wh:{id}:data` (request metadata) and `wh:{id}:body` (raw body bytes — no JSON
escaping or base64 overhead). Request IDs are UUIDv7, so they sort by arrival
time. Storing a request, evicting the oldest beyond `MAX_REQUESTS` /
`MAX_URL_STORAGE` and refreshing the TTL happen in one Lua script. Searches
scan requests in small chunks, so memory stays bounded. A sixth key,
`wh:{id}:audit`, is the URL's record for [abuse handling](#abuse-handling): it
expires `AUDIT_RETENTION` after the others.

## Contributing

Issues and pull requests are welcome. Run `make test` before sending a pull
request. It runs the Go tests with the race detector and the Svelte type
check.

If webhookgo is useful to you, [give it a star](https://github.com/dbaio/webhookgo).
It helps other people find it.

## License

[BSD 2-Clause](LICENSE) © 2026 Danilo G. Baio

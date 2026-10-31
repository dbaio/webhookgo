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
- [Accounts](#accounts): optional sign-in, backed by Postgres
- [Privacy](#privacy): what is kept, who sees it, for how long
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
- **Accounts** (optional): sign in with an email and password, invite people with one-time links, no SMTP needed. Off by default; see [Accounts](#accounts).
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
| `API_RATE_LIMIT` | `1200` | API calls (`/token/…`, `/api/…`) per minute per client IP (`0` = unlimited). |
| `AUDIT_RETENTION` | `180d` | How long the record of a URL is kept after it expires or is deleted (`0` = none). See [Abuse handling](#abuse-handling). |
| `ABUSE_CONTACT` | none | Email or URL shown on the `/abuse` page. |
| `ACCOUNTS` | `off` | `on` enables sign-in; needs Postgres. See [Accounts](#accounts). |
| `DATABASE_URL` | none | Postgres connection, e.g. `postgres://user:pass@host/db?sslmode=require`. Empty = the standard `PGHOST`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` and `PGSSLMODE` variables, which Compose sets for its own Postgres. |
| `POSTGRES_PASSWORD` | `webhookgo` | Password of the Compose Postgres (Compose only). Set it before the first start. |
| `SESSION_TTL` | `30d` | Signed-in sessions end after this long without activity. |
| `LOGIN_RATE_LIMIT` | `10` | Login attempts per minute per client IP (`0` = unlimited); three times that per account and client network (/24, or /56 for IPv6), so nobody can lock you out of your account from elsewhere. |
| `ANONYMOUS_URLS` | `on` | With accounts on, `off` lets only signed-in people create URLs. See [Private URLs](#private-urls). |
| `ACCOUNT_MAX_URLS` | `100` | Private URLs each account may own (`0` = unlimited). |
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

## Accounts

Off by default: a plain `docker compose up -d` has no sign-in and nothing
changes. Switched on, people sign in with an email and a password, and the
app keeps users and sessions in Postgres. There is no open sign-up and no
SMTP to configure: people join through one-time links that an admin hands
them.

### Enabling it

Two lines in `.env`, then `docker compose up -d`:

```sh
# .env
COMPOSE_PROFILES=accounts   # also start Postgres
ACCOUNTS=on                 # and use it
POSTGRES_PASSWORD=change-me
```

`COMPOSE_PROFILES=accounts` adds a `postgres` service to the stack, with no
published port and its data in the `postgres-data` volume. Set
`POSTGRES_PASSWORD` before the first start: Postgres reads it only when it
creates the volume. To change it later, run `ALTER USER webhookgo PASSWORD
'...'` in Postgres and update `.env`. `COMPOSE_PROFILES` and the
`COMPOSE_FILE` of [Hosting](#hosting) are independent.

The schema is created and upgraded by the app at startup, so a new version
just needs `docker compose up -d`. Without Postgres the app refuses to start,
with a message that says so.

### The first admin

While the instance has no admin, each start logs a one-time setup link:

```sh
docker compose logs app | grep setup
# create the first admin: https://hooks.example.com/setup#AbC… (valid 1h)
```

Open it, fill in your email, name and password, and you are signed in as
admin. Without `PUBLIC_URL` the log shows the path only; open it on the
address you use for the app. The token sits in the URL fragment (after `#`),
which browsers never send, so it does not reach proxies or access logs.

### Inviting people

Links are made from the command line and are valid for 72 hours, once:

```sh
docker compose exec app webhookgo users add you@example.com          # invite a user
docker compose exec app webhookgo users add --admin you@example.com  # invite an admin
docker compose exec app webhookgo users reset you@example.com        # password reset link
docker compose exec app webhookgo users list
```

Send the link through a channel you trust: whoever opens it first gets the
account. A reset link also signs the person out everywhere else. The same
commands work with an external Postgres, as long as the environment has
`ACCOUNTS=on` and the connection settings.

Signed in, **Account** in the top-right menu lets you change your name and
password, and sign out of your other devices. Changing the password ends
every other session.

### Admin panel

Admins also have **Admin** in that menu, at `/admin`:

- **Overview**: how many users, admins and disabled accounts there are and
  how many private and public URLs; Redis memory (used against `maxmemory`) and evicted
  keys; the database size; the version.
- **Users**: search; invite (the link is shown once, valid 72 hours); make
  someone an admin or a user; disable (their sessions end and they cannot
  sign in until enabled again); hand out a password reset link; delete an
  account together with its private URLs. The last active admin cannot be
  demoted, disabled or deleted, and admins cannot do any of these to their
  own account.
- **URLs**: every private URL with its owner and activity, and a lookup that
  takes any URL or UUID, anonymous or not, and shows what the instance knows
  about it, including the record `webhookgo audit` prints, with a delete
  button.
- **Reports**: the URLs reported from the `/abuse` page, with their owner,
  record and each report's message; delete the URL, disable its owner or
  dismiss the reports. See [Abuse handling](#abuse-handling).
- **Log**: every admin action, with who did it, from where and when. That
  includes opening someone else's URL, private or anonymous (noted once an
  hour per URL),
  links made from the command line, and the creation of the first admin.
  The log is only ever appended to.

**URLs → Public** lists the anonymous URLs as well, with the IP each was
created from and when it expires; both lists search by name, exact ID or
creator IP. Opening one is logged, as opening someone's private URL is.
To look for abuse, the public list marks the URLs that answer with their
own HTML, XML or SVG page (the usual phishing) and can show only those, can
put the URLs holding the most requests first, and **Top IPs** counts the
addresses that created the most. These read every anonymous URL, so they
take their turn with the other heavy reads.
Anonymous URLs live in Redis only, so with accounts on they are
also indexed in one more Redis key (`wh:anonymous`): at startup the instance
adds those created while accounts were off, and every 10 minutes it drops
those that expired.

The panel runs on its own JSON API, open only to admins signed in with a
session (an API key never reaches it):

| Method | Path | |
| --- | --- | --- |
| `GET` | `/api/admin/stats`, `/api/admin/log` | Instance numbers; the admin log (`page`, `per_page`, `action`, `actor`). |
| `GET` / `POST` | `/api/admin/users` | List accounts (`q`, `page`, `per_page`); invite one: `{"email", "role"}` → `{"link", "expires_at"}`. |
| `PATCH` / `DELETE` | `/api/admin/users/<id>` | Change the role (`{"role"}`) or disable (`{"disabled"}`); delete the account and its URLs. |
| `POST` | `/api/admin/users/<id>/reset` | A password reset link: `{"link", "expires_at"}`. |
| `GET` | `/api/admin/urls` | Every private URL with its owner (`q`, `owner`, `page`, `per_page`); `kind=anonymous` lists the anonymous ones instead (`q`, `pages=1` for those that serve a page, `sort=requests`, `page`, `per_page`). Each has `serves_page`. |
| `GET` | `/api/admin/ips` | The 20 addresses that created the most live anonymous URLs: `{"data": [{"ip", "urls", "pages", "requests", "latest_created_at"}], "urls"}`. |
| `GET` / `DELETE` | `/api/admin/urls/<id>` | What is known about any UUID: `{"token", "owner", "record"}`; delete the URL. |
| `GET` | `/api/admin/reports` | Abuse reports grouped by URL, newest first (`status=open\|actioned\|dismissed\|all`, `page`, `per_page`). |
| `POST` | `/api/admin/reports/resolve` | Close a URL's open reports: `{"token_id", "action": "delete_url"\|"disable_owner"\|"dismiss", "note"}` → `{"updated"}`. |

### Private URLs

A URL created while signed in is private: only its owner and the admins can
open it in the app or through the API (anyone else gets `403`), and it does
not expire. Its settings live in Postgres, so it survives Redis losing them;
the requests it captures stay in Redis and follow `TOKEN_TTL`,
`MAX_REQUESTS` and `MAX_URL_STORAGE` as always. Webhooks can still be sent
to it by anyone: the capture side is public, as it has to be. Each account
may own up to `ACCOUNT_MAX_URLS`. **Your URLs** in the menu lists them, on
any browser, and so does the account page, with each one's requests and a
delete button.

Anonymous URLs still work as before, with one addition: with accounts on,
creating one returns a `claim_key`, once. The app keeps it in the browser's
history, and the URL can be **saved** to an account with it
(`POST /token/<token>/claim` with the key). The key is what keeps someone
who merely knows a URL's ID from taking it over. Signing in from the tab
that created a URL less than an hour before saves it at once, with an undo;
any other URL on the browser is saved only on request, after a warning, as
on a shared computer it may be someone else's (whoever else uses it can
still send requests to it, but no longer see them). In the other direction,
**Make anonymous** in a private URL's settings (`POST /token/<token>/release`)
hands it back with a new `claim_key`, and it expires like any other from
then on.

For a private instance, set `ANONYMOUS_URLS=off`: creating a URL then needs
a sign-in. URLs that already exist keep working.

### API keys

Scripts and CI jobs use an API key instead of a session. **Account → API
keys** makes one, named after where it will be used, and shows it once; the
app keeps only its hash. Send it as `Api-Key: wgo_…` or
`Authorization: Bearer wgo_…` on the `/token…` routes: `POST /token` then
creates a private URL that belongs to you, `GET /token` lists your URLs, and
everything under `/token/<token>` works on the URLs you own. A CI job that
creates a URL, points a service at it and waits for the call:

```sh
ID=$(curl -s -X POST -H "Api-Key: $KEY" "$BASE/token" -d '{"description":"CI run 42"}' | jq -r .uuid)
# ... trigger the webhook at $BASE/$ID ...
curl -s -H "Api-Key: $KEY" "$BASE/token/$ID/wait?timeout=30" | jq .
curl -s -X DELETE -H "Api-Key: $KEY" "$BASE/token/$ID"
```

A key acts as its user with the plain **user** role, even for an admin, so a
leaked key never opens other people's URLs; and it never works on the
`/api/…` routes, so it cannot manage the account, make keys or reach the
admin side. A bad or revoked key gets `401`, never anonymous access. Each
account may hold 20 keys; **Revoke** ends one at once, open live streams
included. Keys start with `wgo_`, so a secret scanner can spot one that
leaked into a repository.

### What is stored

In Postgres: each user's email, name, role, password (as an argon2id hash),
when it was created and last signed in; each session's hash, IP address,
user agent and times, kept until it expires after `SESSION_TTL` without use;
the hashes of unused links; each API key's name, first characters, hash and
when it was last used; and the settings of each private URL, with its
owner. Secrets themselves are never stored.
Captured requests stay in Redis, as before, and the session cookie is
stripped from every captured request before it is recorded, since the UI
itself can call capture URLs. The cookie is `HttpOnly`, `SameSite=Strict`,
and `Secure` with the `__Host-` prefix when the app is served over HTTPS
(`PUBLIC_URL` starts with `https://`, or `TRUST_PROXY` is set and the proxy
says so).

### Backup

```sh
docker compose exec postgres pg_dump -U webhookgo webhookgo > backup.sql
docker compose exec -T postgres psql -U webhookgo webhookgo < backup.sql   # restore, on an empty database
```

> **`docker compose down -v` deletes the volumes**, and with them every
> user and session. `docker compose down` keeps them.

### External Postgres

Any Postgres 14 or later works, managed ones included (no extensions are
needed). Leave the profile off and point `DATABASE_URL` at it:

```sh
# .env
ACCOUNTS=on
DATABASE_URL=postgres://webhookgo:secret@db.example.com/webhookgo?sslmode=require
```

A password with special characters is easier to pass through the standard
`PGHOST`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` and `PGSSLMODE` variables
instead, which the app reads when `DATABASE_URL` is empty.

## Privacy

What an instance keeps, who sees it and for how long. Each instance also
shows this at `/privacy` (in the menu), written from its own settings.

- **Captured requests** hold whatever the sender sent and its IP address, up
  to `MAX_REQUESTS` per URL, and go `TOKEN_TTL` after the URL was last used,
  or at once when it is deleted.
- **Anonymous URLs** are public: whoever has the link sees and changes them.
  [Private URLs](#private-urls) are for their owner and the admins.
- **Who created a URL** (IP address and user agent) is kept with it. For an
  anonymous URL, the abuse page shows it to anyone who looks the URL up
  there, so that abuse can be reported to the network it came from.
  Otherwise only the URL's owner and the admins see it: the API leaves `ip`
  and `user_agent` empty for anyone else.
- **Records** for [abuse investigations](#abuse-handling) last
  `AUDIT_RETENTION` after the URL is gone, and hold no request content.
- **Admins** can list and open every URL to look for abuse. Opening one that
  is not theirs goes into the admin log, which cannot be edited and which
  every admin sees, so keep the admins few.
- **Accounts** keep what [What is stored](#what-is-stored) lists. Expired
  sessions, and links nobody used in time, are deleted every hour. **Abuse
  reports** keep the reporter's IP address, message and contact.
- **No analytics or third-party scripts**: the UI's Content Security Policy
  loads nothing from anywhere else.

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

With [accounts](#accounts) on, a URL that belongs to an account shows on
`/abuse` as private, with none of its details and no delete button: only the
admins can act on it. The page then offers a form that reports the looked-up
URLs, private and active anonymous ones alike, to the admins, who see them in
the panel's **Reports** tab and delete the URL, disable its owner or dismiss
the reports, with a note; each of these is logged. A report keeps the
reporter's IP address, their message and, if they left one, a contact
address. URLs that do not exist are accepted but not stored, an address's
report of a URL is kept once a day, and each address may send ten reports an
hour.

The form posts `{"ids": [...], "message", "contact"}` (up to 50 IDs) to
`POST /api/abuse/reports`, which answers `202` whether or not the IDs exist.

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
an unknown URL answers `404`, a deleted one `410`, and a [private one](#private-urls)
`403` to anyone but its owner and the admins (every `/token/<token>…` route; capture
stays public). A URL's `ip` and `user_agent`, those of whoever created it, are empty
but for its owner and the admins; for an anonymous URL, the abuse page reads them
from `GET /api/abuse/urls/<token>` (`{"ip", "user_agent", "created_at"}`, `403` for a
private URL), with or without accounts.

| Method | Path | |
| --- | --- | --- |
| `ANY` | `/<token>[/<status>][/<path>]` | Capture a request. |
| `POST` | `/token` | Create a URL. JSON or form body: `default_status`, `default_content`, `default_content_type`, `timeout`, `cors`, `description`, `response_headers` (`[{name, value}]`), `signature` (JSON only, see [Signature verification](#signature-verification)). Signed in, the URL is private (`owner_id`, `expires_at: null`); anonymous with accounts on, the answer carries a one-time `claim_key`. |
| `GET` | `/token` | The signed-in user's URLs, newest first: `page`, `per_page` (max 100), `q` (search in descriptions). Each item also has `latest_request_at`. Accounts only. |
| `GET` / `PUT` / `PATCH` / `DELETE` | `/token/<token>` | Read, update (partial), delete a URL. |
| `POST` | `/token/<token>/claim` | Make an anonymous URL yours: `{"claim_key"}`. Accounts only. |
| `POST` | `/token/<token>/release` | Make a private URL anonymous again; the answer carries a new `claim_key`. Owner or admin. Accounts only. |
| `PUT` | `/token/<token>/cors/toggle` | Toggle CORS. |
| `GET` | `/token/<token>/requests` | List requests: `page`, `per_page`, `sorting=newest\|oldest`, `q` (search), `method=GET,POST`, `summary=1`. |
| `GET` | `/token/<token>/request/latest` | Most recent request. |
| `GET` | `/token/<token>/wait` | Wait for a matching request: `since`, `timeout`, `match`, `q`, `method`. See [End-to-end tests](#end-to-end-tests). |
| `GET` / `DELETE` | `/token/<token>/request/<id>` | Read or delete a request. |
| `GET` | `/token/<token>/request/<id>/raw` | The body as received (`?download=1` to download). |
| `DELETE` | `/token/<token>/request` | Delete all requests. |
| `GET` | `/token/<token>/stream` | Live events (Server-Sent Events). |
| `GET` | `/healthz` | Health check (pings Redis). |
| `GET` | `/api/config` | Instance settings for the UI, including `accounts`. |
| `GET` | `/api/me` | The signed-in user, or `{"user": null}`. |
| `POST` | `/api/login`, `/api/logout` | Sign in with `{"email", "password"}` (sets the session cookie) and out. [Accounts](#accounts) only. |
| `POST` | `/api/links/inspect`, `/api/links/redeem` | Look at and use a one-time link: `{"token"}`, plus `email`, `name` and `password` as the link's kind needs. Accounts only. |
| `PATCH` / `POST` | `/api/account`, `/api/account/password`, `/api/account/logout-others` | Rename (`{"name"}`), change the password (`{"current_password", "new_password"}`), end the other sessions. Accounts only. |
| `GET` / `POST` | `/api/keys` | List the signed-in user's [API keys](#api-keys), or make one: `{"name"}`; the answer carries the `key` this once. Session only. |
| `DELETE` | `/api/keys/<id>` | Revoke a key. Session only. |

With accounts on, the `/token…` routes also take an API key, as `Api-Key:
wgo_…` or `Authorization: Bearer wgo_…`, in place of the session; a session
cookie wins when both are present. The `/api/…` and `/token…` routes refuse
state-changing requests that browsers mark as coming from another site.
Requests from scripts, which carry no `Origin` or `Sec-Fetch-Site` header,
are not affected.

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

The tests of [Accounts](#accounts) need a Postgres and are skipped without
one. Point `TEST_DATABASE_URL` at any database you can create schemas in
(each test uses its own, dropped afterwards), as the CI workflow does:

```sh
docker run -d --rm --name pg -e POSTGRES_PASSWORD=test -p 127.0.0.1:5432:5432 postgres:18-alpine
TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:5432/postgres?sslmode=disable' go test -race ./...
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
cmd/webhookgo/      entry point (also `webhookgo healthcheck`, `audit` and `users`)
internal/config/    environment configuration
internal/store/     Redis storage: atomic Lua scripts, hash-tagged keys, rate limiting
internal/accounts/  Postgres storage for users, sessions and one-time links; embedded SQL migrations
internal/auth/      argon2id passwords, secrets and the session cookie
internal/hub/       one Redis pub/sub subscription fanned out to SSE clients
internal/signature/ signature checks for webhook providers
internal/server/    capture, JSON API, accounts API, live stream, embedded static files
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
expires `AUDIT_RETENTION` after the others. With accounts on, a
[private URL](#private-urls) has its settings in Postgres and `wh:{id}:token`
is a cache of them, restored on a miss; `wh:{id}:none` remembers for a minute
an ID that exists nowhere, so guesses do not reach Postgres.

## Contributing

Issues and pull requests are welcome. Run `make test` before sending a pull
request. It runs the Go tests with the race detector and the Svelte type
check.

If webhookgo is useful to you, [give it a star](https://github.com/dbaio/webhookgo).
It helps other people find it.

## License

[BSD 2-Clause](LICENSE) © 2026 Danilo G. Baio

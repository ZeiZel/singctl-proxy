# License admin scripts

Curl-based admin tooling for the singctl license server. The deployed server
runs distroless (no shell), so these scripts — run from your own machine —
are the primary way to manage licenses in production.

## Setup

```sh
export LICENSE_HOST=http://194.87.104.170        # or https://your-domain, default shown
export LICENSE_ADMIN_TOKEN=$(grep '^LICENSE_ADMIN_TOKEN=' /path/to/server/.env | cut -d= -f2-)
```

`LICENSE_ADMIN_TOKEN` must match the `LICENSE_ADMIN_TOKEN` the server was
started with (see the server's `.env` / compose file). `LICENSE_HOST`
defaults to `http://194.87.104.170` if unset.

All scripts require `curl` and `python3` (used only for JSON
encode/decode/pretty-print — no external Python packages needed).

## Scripts

### `gen-keys.sh` — issue pre-generated, unassigned keys

```sh
scripts/license/gen-keys.sh 10                      # 10 perpetual/default-TTL keys
scripts/license/gen-keys.sh 5 --days 365             # 5 keys valid for 365 days
scripts/license/gen-keys.sh 1 --subject alice@x.com  # single key, pre-assigned subject
```

Prints one signed license token per line on stdout (pipeable, e.g. to a file
or `pbcopy`). Keys are unassigned (`subject` optional) until a client calls
`POST /v1/activate`, which binds the key to a device (and optionally an
email) — last activation wins.

### `list-keys.sh` — list issued keys

```sh
scripts/license/list-keys.sh                  # all keys
scripts/license/list-keys.sh --activated      # only keys bound to a device
scripts/license/list-keys.sh --unactivated    # only keys never activated
scripts/license/list-keys.sh --json           # machine-readable JSON
```

Human-readable output columns: `ID SUBJECT EMAIL DEVICE STATUS CREATED`.

### `reset-key.sh` — clear a device binding

```sh
scripts/license/reset-key.sh <license-id>
```

Clears `device_id`/`activated_at`/`email` on the given license id so it can
be activated on a new device (e.g. customer got a new machine). Use
`list-keys.sh` to find the id.

## Notes

- These scripts only ever hit the public `/v1/activate` indirectly (via
  customers' clients) — they call the **admin** endpoints
  (`/v1/admin/issue`, `/v1/admin/licenses`, `/v1/admin/reset`), all gated by
  `LICENSE_ADMIN_TOKEN`.
- Treat `LICENSE_ADMIN_TOKEN` like a root password: don't commit it, don't
  paste it into shared shells.

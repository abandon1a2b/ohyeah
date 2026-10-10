# Authentication And Authorization

Ohyeah supports personal local operation and a central service with multiple users. Central deployment currently uses a single service instance and SQLite; authentication does not make the storage or collector scheduler ready for multiple replicas.

## Personal Mode

`auth.mode: local` is the default. It requires `server.address` to bind an explicit loopback IP, such as `127.0.0.1:8787` or `[::1]:8787`. It grants the local operator all permissions. Do not publish this mode through an unauthenticated proxy. The native Go CLI operates directly on local state and is intended for the trusted host operator; it does not apply HTTP user permissions.

## Central OIDC Setup

An external OIDC identity service is required. Ohyeah uses `github.com/coreos/go-oidc/v3/oidc` and `golang.org/x/oauth2` as a relying party, not as an identity provider. The repository includes a signed local mock provider for tests, not a production account/password server.

Register a web client at the identity service with this exact callback:

```text
https://memory.example.com/auth/callback
```

Enable Authorization Code flow and S256 PKCE. Configure the service:

```yaml
server:
  address: 0.0.0.0:8787
auth:
  mode: oidc
  public_url: https://memory.example.com
  session_ttl: 8h
  token_ttl: 24h
  oidc:
    issuer: https://identity.example.com/realms/work
    client_id: ohyeah
    client_secret: ""
  principals:
    - subject: "stable-provider-subject-for-admin"
      name: Administrator
      admin: true
    - subject: "stable-provider-subject-for-reader"
      name: Reader
      projects: [example]
      sync_projects: []
    - subject: "service:collector"
      name: Collector service account
      projects: [example]
      sync_projects: [example]
```

Set `OHYEAH_AUTH_OIDC_CLIENT_SECRET` in the service environment when the provider requires a confidential client secret. Do not put it into chat, downloads, frontend code, or source control. Viper reads this environment variable directly; no environment file loader is needed.

`public_url` must be the canonical external origin without a path. Terminate HTTPS at a trusted reverse proxy and restrict direct access to the internal HTTP listener. Issuer, discovery endpoints, and public URL require HTTPS; literal loopback HTTP URLs are permitted for development. Request Host headers do not influence callback URLs or generated client configuration.

Subjects are stable `sub` identifiers from the configured issuer. Email addresses and display names do not grant permissions. Unknown subjects are denied; a verified but unauthorized login logs its issuer and subject so the operator can add a binding. Empty `principals` permits no users. Change auth configuration and restart the service to apply it. OIDC discovery failure prevents HTTP startup; authentication never silently falls back to local mode.

Changing the configured issuer invalidates existing Ohyeah credentials. Existing principals and permissions are checked on every request using the startup configuration. Provider account disablement or provider logout does not immediately revoke existing Ohyeah credentials: revoke them locally or let their fixed lifetime expire.

## Browser Login And CLI Pairing

Web UI login redirects to the provider. Ohyeah verifies signature, issuer, audience, expiry, nonce, one-time state and browser binding. Code exchange uses PKCE. It then creates an opaque Ohyeah session in an HttpOnly, SameSite=Lax cookie; the cookie is Secure when the canonical origin uses HTTPS. Mutating cookie-authenticated requests require a matching Origin and session-bound CSRF token. No provider access token or ID token is accepted as an Ohyeah API bearer token.

Python and Node.js clients use:

```bash
python3 ohyeah.py --server https://memory.example.com login
node ohyeah.mjs --server https://memory.example.com login --no-browser
```

The CLI prints an authorization URL and a code, then polls for up to ten minutes. The browser performs OIDC login at the service's fixed callback. The user must compare the browser code with the CLI and explicitly approve. The CLI holds a separate secret that never appears in the browser URL; approval alone cannot retrieve a token. Approval and exchange are single-use. This application pairing protocol is not an implementation of the OIDC Device Authorization Grant.

Because the browser returns to the central service, the CLI can run remotely without opening a local callback port. Login does not print tokens. The resulting API token inherits the user's configured account permissions, including administration when applicable; it is not a scope-selection interface. Use a separate limited principal for an unattended Agent.

Credentials are saved per exact service URL under `~/.config/ohyeah/credentials/`, with owner-only files on Unix. Python and Node.js share the format. Set `OHYEAH_CREDENTIALS_DIR` to override the directory. Use operating-system ACLs to protect it on Windows. Use the canonical service URL consistently. `OHYEAH_TOKEN` takes precedence over saved credentials. Authenticated remote requests require HTTPS, and clients reject HTTP redirects rather than forward credentials.

Tokens have a fixed expiry (`token_ttl`, default 24h, maximum 30 days). Browser sessions default to 8h and are capped at 24h. There is no automatic refresh in this increment. Expired tokens require login again. The database stores credential hashes, issuer, subject, ID and expiry; it never stores raw session or bearer tokens. Temporary OIDC flows contain PKCE verifiers and expire after ten minutes. Sessions, revocation and pairing state survive service restart through SQLite.

```bash
python3 ohyeah.py whoami
python3 ohyeah.py tokens
python3 ohyeah.py revoke <credential-id>
python3 ohyeah.py logout
```

`tokens` lists the current subject's active browser and CLI credentials without secrets. Users can revoke their own credentials; administrators can revoke any known credential ID. `logout` revokes the presented credential, then deletes its saved local file. If `OHYEAH_TOKEN` is supplied, logout revokes that token too, but cannot remove it from the parent process environment.

## Permissions

| Action | Required permission |
|---|---|
| Search | Project in `projects`; OIDC search requires explicit `project` |
| Get memory | Memory's project in `projects`; unauthorized IDs return 404 |
| List projects / sources | Filtered by `projects`; collector command options and cursors hidden from nonadmins |
| List sync runs | Nonadmins must specify an authorized source; global listing is admin-only |
| Trigger one registered collector | Source's project in `sync_projects` |
| Trigger all collectors | `admin: true` |
| Configuration read/write/validate | `admin: true` |
| Index diagnostics / rebuild | `admin: true` |
| Global statistics / storage paths | `admin: true`; nonadmins receive limited service availability metadata |
| Issue machine credentials | `admin: true`, for a preconfigured principal |

The health probe, authentication entry points, static assets and downloadable clients are public. They contain no user or project data. The server logs credential issuance/revocation and synchronization requests with subject and credential/source IDs, never bearer tokens. Deployments should retain these logs; a dedicated persistent audit-query API is not included yet.

Administrators can issue a token for a configured service principal via `POST /auth/tokens` with JSON `{"subject":"service:collector","name":"collector-node-1"}`. Browser requests require the Origin and CSRF token returned by `/auth/me`; bearer-authenticated administration uses `Authorization: Bearer ...`. The response includes the raw token once. Set that token as `OHYEAH_TOKEN` for the service account. `DELETE /auth/tokens/<id>` revokes it immediately.

Remote command execution is not exposed: synchronization invokes an already registered trusted collector. Changing collector commands requires administration. Central server collection and remote collector uploads are both supported. `sync_sources` grants individual collector triggers; `upload_sources` grants writes to exact upload mounts, additionally checked against their configured writer. See [Remote Collector Uploads](uploads.md) for batching, idempotency, cursor concurrency and deletion boundaries.

## Verification

```bash
go test -race ./...
go vet ./...
npm run check
```

OIDC tests run a local RSA-signed mock identity provider and real HTTP callbacks. They cover PKCE, wrong signatures/issuer/audience/nonce, expiry, unknown accounts, state/browser binding and replay, project isolation, administrative boundaries, CSRF, machine tokens, revocation, credential persistence and single-use pairing. Both downloaded scripts execute login, browser approval, identity lookup and logout. No external identity service is required to run the tests. A real provider must still be configured and verified before central deployment.

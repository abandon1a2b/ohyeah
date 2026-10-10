# Local Central-Service Demo

This optional setup supplies a real Keycloak identity provider for trying single-instance multiuser Ohyeah. It is localhost-only, uses public demo passwords, and is not a production deployment. Ohyeah itself still runs as a native process; Docker is only used for the identity service.

From the repository root:

```bash
docker compose -f deploy/oidc-demo/compose.yaml up -d
mkdir -p tmp/central-demo
node deploy/oidc-demo/config.mjs > tmp/central-demo/config.yaml
make build
./ohyeah --config tmp/central-demo/config.yaml serve
```

Wait for identity discovery at `http://127.0.0.1:18080/realms/ohyeah/.well-known/openid-configuration` before starting Ohyeah. Its managed Meilisearch uses port 7702 and may download the pinned binary on first start. State, runtime files and configuration live under the project's ignored `tmp/central-demo` directory. The existing personal service on 8787 and its data are unaffected.

Open `http://127.0.0.1:8788` and log in:

| User | Password | Ohyeah access |
|---|---|---|
| admin | admin-demo-only-local | Administration |
| reader | reader-demo-only-local | Read demo project |

The Keycloak management console is `http://127.0.0.1:18080/admin`, with username `demo-admin` and password `demo-admin-only-local`. Realm user subjects are preassigned to match the demo configuration. If an imported provider version generates different subjects, the verified unauthorized subject is logged by Ohyeah; update the demo principal binding accordingly.

As admin, open Access Management to add user/service-account permissions, sign a token for `service:demo-node`, and inspect audit records. Download a client from `/downloads/python.zip` or `/downloads/node.zip`. Set the issued machine credential as `OHYEAH_TOKEN` in the collector's process environment, then prepare and upload a batch for `demo/remote/node`; see [uploads.md](../../docs/uploads.md).

CLI users can instead run `login` and authorize in the browser. The admin login belongs to an administrator, so its CLI token has administrative permissions; use the dedicated service account for routine remote collection.

Stop the identity service with:

```bash
docker compose -f deploy/oidc-demo/compose.yaml down
```

The named identity volume preserves accounts between runs. Realm startup imports are skipped when the realm already exists. Use separate strong credentials, HTTPS, a production identity database and a properly configured confidential client for an actual central service. Official setup references: [Keycloak Docker quickstart](https://www.keycloak.org/getting-started/getting-started-docker), [realm import](https://www.keycloak.org/server/importExport).

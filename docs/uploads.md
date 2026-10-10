# Remote Collector Uploads

A single Ohyeah service can execute trusted central collectors and receive remote collector output concurrently. Configure separate mounts for each writer; never mix two authoritative collectors in one snapshot source.

```yaml
projects:
  work:
    workspace: /logical/workspace
    types:
      remote:
        collector:
          mode: upload
          revision: 1
        mounts:
          laptop:
            root: /logical/documents
            revision: 1
            writer: service:laptop
auth:
  # Keep the normal OIDC issuer/client/public_url configuration.
  principals:
    - subject: service:laptop
      upload_sources: [work/remote/laptop]
      projects: [work]
```

Upload-only workspaces and roots are logical absolute paths and do not need to exist on the central server. An upload type must not declare a command. A mount requires exactly one writer subject; administrators grant its `upload_sources` permission separately. Personal local mode uses writer `local`. Existing command collectors and configurations retain their behavior. `sync_sources` grants permission to trigger individual central sources without granting project-wide synchronization. Upload mounts are skipped by the periodic scheduler and filesystem watcher.

## Client Workflow

After login, or with an administrator-issued `OHYEAH_TOKEN` for the configured writer:

```bash
python3 ohyeah.py upload-state work/remote/laptop
python3 ohyeah.py prepare-upload work/remote/laptop --file output.ndjson > batch.json
python3 ohyeah.py upload work/remote/laptop --file batch.json
```

Alternatively execute a trusted collector locally, without a shell:

```bash
node ohyeah.mjs prepare-upload work/remote/laptop \
  --collector /absolute/path/to/collector \
  --collector-args '["collect"]' --root /absolute/local/documents > batch.json
node ohyeah.mjs upload work/remote/laptop --file batch.json
```

The collector receives the versioned stdin request, current source cursor and public mount options. `--root` changes the client's working directory and request paths; it does not change central ownership or scope. `--collector-timeout` defaults to 300 seconds and is capped at one hour. Collector output is limited to 24 MiB by clients. Split large imports into delta batches; the HTTP upload body limit is 32 MiB. Both language clients support the same commands.

Keep the prepared batch file until receiving a commit receipt. On a network error, resend the identical file. Do not prepare a fresh snapshot just to retry an uncertain request.

## Transaction And Conflict Rules

`GET /api/v1/uploads/state?source=work/remote/laptop` returns a consistent version, scope revision, cursor and collector request. `POST /api/v1/uploads` accepts the `pkg/collector.UploadBatch` JSON structure:

```json
{
  "protocolVersion": 1,
  "sourceId": "work/remote/laptop",
  "batchId": "unique-stable-batch-id",
  "expectedVersion": 0,
  "scopeRevision": "value-from-upload-state",
  "envelopes": [
    {"type":"document","document":{"externalId":"a","units":[{"key":"final","content":"verified result"}]}},
    {"type":"complete","mode":"snapshot","cursor":{"offset":1}}
  ]
}
```

The protocol validator and materializer are shared with central command collectors. The server derives storage IDs, project, mount and source provenance; clients cannot choose internal IDs. A complete envelope is mandatory. Relationships must refer to units in the batch. Snapshot batches cannot include explicit deletes; a delta cannot both update and delete the same document.

SQLite commits documents, corrections, deletions, cursor, version, sync run, index outbox, receipt and an upload audit event in one transaction. The version changes only on a successful new batch. The exact batch ID and canonical request hash return the original receipt on retry. Reusing an ID with different contents returns 409. Receipts persist across restart.

Concurrent batches with the same expected version have one winner; others return 409. Scope changes (including collector/mount revision or writer transfer) invalidate prepared new batches. Resolve 409 by reading current state and preparing a genuinely new batch against the latest cursor and scope. A snapshot deletes missing documents only within its own source, never another source or writer. Each device should normally have its own mount.

If Meilisearch is unavailable after the SQLite commit, the API returns 202 with `indexPending: true`. The upload is committed: do not invent a replacement batch. Its durable outbox is retried by normal synchronization; resending the original file also attempts delivery. HTTP 200 means the commit and indexing attempt completed. HTTP 401/403 is an identity/ownership failure, 409 a concurrency/idempotency conflict, and 422 invalid collector protocol.

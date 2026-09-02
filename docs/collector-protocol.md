# Collector Protocol

The collector protocol lets any executable provide work-memory records without changing or importing ohyeah internals. The transport is one JSON request on stdin and newline-delimited JSON envelopes on stdout.

## Invocation

Ohyeah starts the configured executable directly, without a shell:

```text
executable [configured arguments...]
```

The process working directory is the source root. `OHYEAH_COLLECTOR_PROTOCOL=1` is present in the environment. A collector should write only protocol envelopes to stdout and send diagnostics or ordinary logs to stderr.

Collectors execute with the current user's permissions. Only register trusted executables.

Ohyeah enforces the configured collector timeout. On cancellation or protocol failure it closes the output stream, terminates the collector process group where supported, and reaps the process.

## Request

The first stdin line is:

```json
{
  "protocolVersion": 1,
  "project": {
    "id": "example-project",
    "workspace": "/absolute/path/to/workspace"
  },
  "type": {
    "id": "markdown",
    "revision": 1
  },
  "mount": {
    "id": "team-docs",
    "root": "/absolute/path/to/workspace/doc",
    "revision": 1,
    "options": {
      "profile": "team"
    }
  },
  "cursor": {
    "offset": 42
  },
  "maxFileSize": 268435456
}
```

`command` and `args` belong to the data type and are not exposed in `mount.options`. The cursor belongs to the mount and is the value returned by its last successful run.

## Document Envelope

Each collected object is one line:

```json
{
  "type": "document",
  "document": {
    "externalId": "ticket/EXAMPLE-123",
    "sourceType": "requirement",
    "modifiedAt": "2026-08-27T09:00:00Z",
    "reference": {
      "url": "https://example.invalid/ticket/EXAMPLE-123"
    },
    "units": [
      {
        "key": "final-decision",
        "kind": "decision",
        "title": "Final routing decision",
        "content": "Use the verified upstream event.",
        "occurredAt": "2026-08-27T08:30:00Z",
        "status": "confirmed",
        "requirementIds": ["EXAMPLE-123"]
      }
    ]
  }
}
```

`externalId` must be stable and unique within the mount. `sourceType` describes the semantic origin, such as `requirement`, `markdown`, or `codex_thread`; it is independent of the command driver and configured type ID. Every unit requires a stable `key`. Ohyeah derives global IDs and content hashes; collectors must not generate storage IDs.

Useful unit kinds include `request`, `conclusion`, `correction`, `decision`, `change`, `verification`, `todo`, and `document`. Unknown kinds are retained so collectors can evolve independently.

## Relationships

Relationships refer to unit keys. References default to the current document and may explicitly name another document from the same run:

```json
{
  "fromKey": "correction",
  "toExternalId": "ticket/EXAMPLE-123",
  "toKey": "old-conclusion",
  "type": "corrects",
  "confidence": 1
}
```

Ohyeah rejects a run when a relationship references an unknown unit.

## Diagnostics

Structured diagnostics are optional:

```json
{"type":"diagnostic","level":"warn","message":"one source record was unavailable"}
```

Diagnostics do not change synchronization success.

## Completion And Cursors

A successful run must end with exactly one completion envelope:

```json
{"type":"complete","mode":"delta","cursor":{"offset":84}}
```

The completion envelope is the commit boundary:

- Without it, the run fails and ohyeah does not delete unseen objects or advance the cursor.
- `mode: "snapshot"` means the output is the complete source state. Objects not emitted by the run are deleted.
- `mode: "delta"` means the output contains only changes. Objects not emitted are retained.
- A delta collector deletes an object explicitly with `{"type":"delete","externalId":"stable-id"}`.
- After either successful mode, the new cursor is stored.
- Re-emitting the same stable IDs and content is safe.

Collectors should use at-least-once semantics: derive the next request from the supplied cursor, but tolerate processing the same input again after an interrupted run.

## Go Types

Go collectors may import the transport types from:

```go
import "github.com/abandon1a2b/ohyeah/pkg/collector"
```

The protocol itself is language-neutral; importing the Go package is optional.

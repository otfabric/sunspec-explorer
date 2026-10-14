# JSON Schemas

The SunSpec Explorer publishes a JSON Schema (draft 2020-12) for everything it writes to a file or sends over its API. The Go types in `internal/model` are the source of truth; the tests validate real output of the engine and of the daemon against these files (`internal/engine/schema_test.go`, `internal/engine/schema_entities_test.go`, `internal/api/schema_test.go`).

| File | Describes | `$id` |
|---|---|---|
| `snapshot-1.schema.json` | The snapshot export: one read of a device (`otfabric.sunspec-explorer.snapshot/1`). Also the answer of `POST /api/v1/sessions/{id}/snapshot`. | `https://otfabric.io/schemas/sunspec-explorer/snapshot-1.schema.json` |
| `recording-1.schema.json` | **One line** of a recording (`otfabric.sunspec-explorer.recording/1`, JSON Lines): a `header`, `sample`, `event` or `footer` line, told apart by `type`. | `https://otfabric.io/schemas/sunspec-explorer/recording-1.schema.json` |
| `api-1.schema.json` | Every entity of the HTTP API v1 and of the `Service` interface, as named definitions under `$defs`. The document itself constrains nothing: reference a definition, for example `api-1.schema.json#/$defs/SessionInfo`. | `https://otfabric.io/schemas/sunspec-explorer/api-1.schema.json` |

All timestamps are UTC in RFC 3339. All register addresses are zero-based Modbus addresses. A field described as omitted is absent, not `null`; a field that can be `null` says so.

## Definitions of `api-1.schema.json`

| Definition | Where it appears |
|---|---|
| `Health` | `GET /api/v1/health` |
| `TargetConfig`, `SerialConfig` | Body of `POST /api/v1/sessions` |
| `SessionInfo`, `SessionList` | `POST /api/v1/sessions`, `GET /api/v1/sessions/{id}`, `POST .../rediscover`; the list is `GET /api/v1/sessions` |
| `Target`, `DeviceIdentity`, `ModelInfo`, `ProbeAttempt` | Parts of `SessionInfo` |
| `SessionState`, `PollState` | `GET /api/v1/sessions/{id}/state` |
| `SnapshotRequest`, `Snapshot` | Body and answer of `POST /api/v1/sessions/{id}/snapshot` |
| `ModelResult`, `GroupView`, `PointView`, `Address`, `AppInfo`, `LibInfo` | Parts of `Snapshot` and `Sample` |
| `PollConfig` | Body of `PUT /api/v1/sessions/{id}/polling` |
| `Sample` | One poll cycle: in `SessionState` and in `sample` events |
| `Event` | The `data:` field of a server-sent event of `GET /api/v1/events` (kinds `sample`, `session_status`, `discovery_changed`, `recording_status`, `error`, `resync`) |
| `RecordingConfig`, `RecordingInfo`, `RecordingList` | Body and answer of `POST /api/v1/sessions/{id}/recordings`; `POST /api/v1/recordings/{rid}/stop`; the list is `GET /api/v1/recordings` |
| `ExportRequest`, `ExportFile` | Body and answer of `POST /api/v1/sessions/{id}/export` |
| `SerialPort`, `SerialPortList` | `GET /api/v1/serial-ports` |
| `Error`, `ErrorResponse` | `ErrorResponse` (`{"error": Error}`) is the body of every answer with status 400 or above |

`GET /api/v1/recordings/{rid}/download` answers with the recording itself (`application/x-ndjson`): see `recording-1.schema.json`. With `?format=csv` it answers with CSV, which has no schema here; the columns are listed in `docs/ARCHITECTURE.md`, section 7.

The sidecar `<name>.meta.json` that the engine keeps next to a recording is internal (counters and state for the list of recordings, rewritten by the engine): it has no published schema and is not part of a download.

## Shared definitions

`Address`, `AppInfo`, `LibInfo`, `Target`, `DeviceIdentity`, `ModelInfo`, `ModelResult`, `GroupView` and `PointView` appear in more than one file. Each file is self-contained (no reference leaves the file), so these definitions are copies; `TestSchemasShareDefinitions` fails when a definition with the same name differs between two files. Change them in every file at once.

## Validating

With [check-jsonschema](https://check-jsonschema.readthedocs.io) (`pipx install check-jsonschema` or `brew install check-jsonschema`):

```sh
# A snapshot export.
check-jsonschema --schemafile docs/schema/snapshot-1.schema.json snapshot.json

# A recording: the schema describes one line, so validate line by line.
split -l 1 -a 6 recording.jsonl line.
check-jsonschema --default-filetype json --schemafile docs/schema/recording-1.schema.json line.*

# An API entity: reference its definition from a one-line schema next to api-1.schema.json.
echo '{"$ref": "api-1.schema.json#/$defs/SessionInfo"}' > docs/schema/session-info.schema.json
check-jsonschema --schemafile docs/schema/session-info.schema.json session.json
```

A recording that was interrupted (no `footer` line) may end in a truncated line; drop that line before validating.

With [ajv](https://ajv.js.org) (`npm install -g ajv-cli ajv-formats`), which reads draft 2020-12 with `--spec=draft2020`:

```sh
ajv validate --spec=draft2020 -c ajv-formats -s docs/schema/snapshot-1.schema.json -d snapshot.json
```

The schemas use `format: date-time`; a validator checks it only when told to (check-jsonschema does by default, ajv with `ajv-formats`).

## Versioning

The `-1` in a file name is the major version of the format, the same number as in the schema identifier (`.../snapshot/1`, `.../recording/1`) and as the major part of `api_version` in `Health`.

- Additive changes keep the number and are made in place: a new optional property, a new definition, a new value of an enumeration (an event kind, an error code, a stop reason), a new feature name. Validate against the latest published file of the major version, and write readers that ignore what they do not know.
- A change that makes existing documents invalid, or changes what an existing field means, gets a new file (`snapshot-2.schema.json`) and a new schema identifier; the old file stays.

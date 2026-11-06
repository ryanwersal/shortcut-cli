# Shortcut CLI (`sc`)

A Go CLI for Shortcut, following [wand](https://github.com/ryanwersal/wand)'s conventions:
JSON by default, versioned response envelopes, structured errors, YAML/table output,
local agent discovery, bounded requests, and mise development tasks.

All **143 operations** in the pinned Shortcut REST v3 OpenAPI contract are available.
Commands are separated into `read` and `write` namespaces. The CLI never prompts.

## Build and configure

```sh
mise install
mise run check
mise run build
export PATH="$PWD/bin:$PATH"
export SHORTCUT_API_TOKEN='...'
sc auth check
```

Create a **v3** token in [Shortcut account settings](https://app.shortcut.com/settings/account/api-tokens).
The token is read only from the environment and sent in the `Shortcut-Token` header.
There is no token flag, credential file, or configurable destination host. Redirects are rejected.

This tool uses the stable [v3 API](https://developer.shortcut.com/api/rest/v3).
[v4](https://developer.shortcut.com/api/rest/v4) is beta, uses different tokens and
response shapes, and is not supported by this version.

## Read operations

```sh
sc read stories get --story-public-id 123
sc read stories search --query 'owner:me !is:archived' --limit 100
sc --output table read epics list
sc read workflows list
sc read members list
sc read stories list-comments --story-public-id 123
sc read stories history --story-public-id 123
sc read stories query --body '{"workflow_state_types":["started"]}'
```

`read stories query` uses Shortcut's read-only POST search endpoint. Classification
is based on operation semantics, not just HTTP method.

`read stories search` follows pagination with `--limit` (1–1000), `--page-size`
(1–250), `--max-pages`, and `--cursor`. Its metadata includes `count`, `requests`,
`truncated`, and `next_cursor`. To resume, pass `meta.next_cursor` as `--cursor`
and retain the original `--query`. Shortcut limits searches to 1,000 results;
narrow your query for larger workspaces. Pagination links are restricted to the
same API host and search endpoint, and repeated cursors are rejected.

All other operations return **one API response**, preserving the upstream JSON,
including any paging fields. For example, `read search stories` exposes the raw
single-page search operation, and `read epics list-paginated` exposes paged epics.
Use each command's help/schema for its continuation parameters. The CLI does not
silently aggregate or truncate these responses.

## Writes require explicit authorization

Every write requires **both** the `write` namespace and `--execute`. There is no
configuration or environment variable that implicitly enables writes.

```sh
# Validate and preview locally: no credentials or network request required.
sc write stories create --dry-run \
  --body '{"name":"Investigate timeout","workflow_state_id":123}'

# Actually create the story.
sc write stories create --execute \
  --body '{"name":"Investigate timeout","workflow_state_id":123}'

# Update only the supplied fields; JSON null is preserved.
sc write stories update --story-public-id 456 --execute \
  --body '{"workflow_state_id":789,"estimate":null}'

sc write stories create-comment --story-public-id 456 --execute \
  --body '{"text":"Investigation complete."}'

# Deletion uses the same explicit write gate.
sc write stories delete --story-public-id 456 --execute
```

Without `--execute` or `--dry-run`, a write fails before sending any request.
Those flags are mutually exclusive. `--read-only` rejects **all** write commands,
including previews. `--schema` is always local discovery. Writes are never retried
automatically, including on timeouts, rate limits, and server errors. A failed
connection can leave a write's outcome uncertain; inspect the workspace before
issuing it again. Dry-run validates the request shape, not server permissions,
resource existence, or business rules requiring workspace state.

JSON bodies can be supplied through `--body`, `--body-file path.json`, or
`--body-file -` for stdin. Unknown fields, missing required fields, and incorrect
types fail locally. `--schema` documents the accepted fields. Query arrays use
repeated flags. Path/query flags use hyphens; JSON properties use the API's original
underscores. Payloads and uploads are limited to 10 MiB per operation.

```sh
sc write stories update --story-public-id 456 --dry-run --body-file update.json
sc write stories create-reaction --story-public-id 456 --comment-public-id 12 \
  --execute --body '{"emoji":"thumbsup"}'
sc write files upload --file screenshot.png --story-id 456 --execute
```

The command catalog covers categories, custom fields, documents and links,
entity templates, epic workflows, epics and health, external links, files,
teams (called `groups` in v3), integrations, iterations, key results, labels,
linked files, members, milestones, objectives, projects, repositories, search,
stories, comments, reactions, history, subtasks, tasks, bulk operations,
story links, and workflows. Follow API names in `--help` and schema discovery.

## Agent contract and output

```sh
sc agent schema
sc agent schema --operation createStory
sc write stories create --schema
sc agent openapi
sc completions zsh
sc --read-only --compact read stories get --story-public-id 123
```

`agent schema` lists every API command, its operation ID, method, safety class,
and execution requirement. Select an operation to retrieve its input JSON Schema
and response definitions. `agent openapi` prints the original vendored document.
Discovery and completion generation work without credentials.

Successful JSON and YAML responses use:

```json
{"schema_version":"1","data":{},"meta":{}}
```

API fields are preserved in `data`; `meta` identifies the operation and safety.
HTTP 204 responses use `data: null`. Errors go to stderr; stdout contains only
successful output. Table-mode errors remain JSON, matching wand. JSON is pretty
printed unless `--compact` is supplied.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | API, response, or I/O failure |
| 2 | Invalid input, configuration, or missing write authorization |
| 3 | Authentication or authorization failure |
| 4 | Not found |
| 5 | Rate limit or transport failure |

HTTP requests default to a 30-second timeout and a 10 MiB response cap. Read
responses with HTTP 429/5xx may retry twice, respecting bounded `Retry-After`
delays. Set `--timeout`, `--retries`, or `--max-response-bytes` to adjust these.
Aggregated story search also has a 50 MiB total response cap.

## Development

```sh
mise run check       # formatting, go vet, race-enabled tests
mise run coverage
mise run build       # bin/sc
mise run audit       # govulncheck
```

Cobra supplies parsing, help, and completions. The pinned OpenAPI contract drives
command registration, parameter flags, validation, and agent discovery. This
keeps reads/writes and schema documentation aligned without maintaining a second
handwritten API surface. The HTTP client, contract, output, and CLI live in
separate internal packages.

`internal/contract/openapi.json` was retrieved from
[Shortcut's OpenAPI document](https://developer.shortcut.com/api/rest/v3/shortcut.openapi.json)
on 2026-11-06. Update this file deliberately and review command/schema changes.
Tests assert operation coverage and compile every input schema. The local adapter
converts OpenAPI `nullable` to JSON Schema and adds the documented exactly-one-of
`workflow_state_id`/`project_id` rule for story creation, absent upstream.

GitHub Actions checks Linux, macOS, and Windows, uploads coverage, and runs a
separate vulnerability scan. Dependabot checks Go dependencies and action pins.
Tests use local HTTP fixtures and never need a Shortcut token or modify a workspace.

# Allspeak Catalog Backend

## Overview

- Go service that lets prepared Allspeak sessions (film dub audio + subtitles) be distributed online instead of via AirDrop: an authenticated JSON API over a catalog of sessions, with file payloads stored in Cloudflare R2 and transferred exclusively through presigned URLs.
- Problem it solves: today a session is assembled on the Mac and must be hand-delivered to the iPhone near the computer. With the catalog, the Mac uploads once; the phone imports from anywhere later.
- Consumers: an upload client on the Mac (curl or any HTTP client - out of scope) and the Allspeak iOS app (separate future plan). This plan delivers the backend repo, its CI, and its deploy tooling only.
- A session in the catalog = `title` + monotonically increasing `revision` + a manifest of files: 1+ audio tracks and exactly one subtitle file. Content is addressed by sha256 in R2, so publishing a new revision uploads only new files and interrupted uploads are resumable by re-requesting upload URLs.

### Non-goals (v1)

- No iOS work of any kind (separate plan in the Allspeak app repo).
- No upload script/CLI for the Mac - the deliverable boundary is the HTTP API itself.
- No ShazamKit catalog / DTW-map file kinds - manifest carries audio tracks + subtitle only.
- No token issuance/rotation system - two static tokens from env.
- No revision history - only the latest manifest is stored; `revision` is just a monotonic int.
- No garbage collection of unreferenced R2 objects (storage is ~$0.015/GB - revisit when noticeable).
- No optimistic locking / multi-writer support - single trusted writer.
- No landing page, no deeplinks, no public unauthenticated access.

### Rejected alternatives

- **Proxying file bytes through the API** - rejected: request-body limits on the proxy chain, droplet egress/bandwidth becomes the bottleneck, and it breaks at the future public-landing stage. Presigned URLs keep the droplet on the JSON control plane only.
- **No API, manifests directly in the bucket** - rejected: no place for versioning logic, S3 credentials would ship inside the iOS app, dead end for the landing stage.
- **`draft` status on sessions** - rejected as unnecessary: the upload-URL endpoint never touches the catalog, and a session/revision appears only via one atomic finalize call, so half-uploaded content is invisible by construction.
- **DigitalOcean Spaces** - rejected: $5/mo fixed + capped egress vs R2 free tier + zero egress forever.
- **Normalized tracks table in SQLite** - rejected: nobody queries inside a manifest, single writer, read whole - one JSON column is simpler.
- **CGO sqlite driver (mattn)** - rejected in favor of `modernc.org/sqlite` to keep the binary static for a scratch image.

## Skills to invoke

Load each skill below with the Skill tool and follow its conventions before implementing any task in this plan.

- `go` - signature/visibility/structure conventions and the per-task quality gate for all Go code in this repo

## Context (from discovery)

- Fresh repo (`allspeak-catalog`), module `github.com/pkarpovich/allspeak-catalog`, Go 1.25. Image: `ghcr.io/pkarpovich/allspeak-catalog`.
- Deploy target is the existing droplet (`lasso`, ssh alias). Its conventions, captured here so no external lookup is needed:
  - Each project is a git checkout at `~/<repo-name>/` on the droplet with `compose.yaml` at the repo root.
  - A Traefik v3.5 gateway (separate compose project) terminates TLS. Services join the external docker network `proxy` and self-register via labels: `traefik.enable=true`, ``traefik.http.routers.<name>.rule=Host(`${DOMAIN}`)``, `traefik.http.routers.<name>.entrypoints=web-secure`. DNS is on Cloudflare; public certs are valid (Cloudflare edge).
  - Deploys run via Spot (umputun/spot): `spot.yml` with a single task named `deploy` (git clone if missing, git pull, `docker compose pull`, `docker compose up -d`), `inventory.yml` with the host (`lasso`), and a Makefile target `deploy_%: spot -t $* -v -i ./inventory.yml -k $(SSH_KEY)` with `SSH_KEY ?= $(HOME)/.ssh/id_ed25519` - the operator overrides `SSH_KEY` via env; no machine-specific value is baked into the repo.
  - Existing env-var naming precedent for R2 on this droplet: `CF_ACCESS_KEY_ID`, `CF_ACCESS_SECRET`, `CF_ENDPOINT`, `CF_BUCKET`.
- Real payload sizes (measured on actual prepared films): m4a track ~67-75MB, srt ~100-160KB, session total ~70-230MB. R2 single-PUT limit (5GB) is far above a track; presigned uploads bypass the Cloudflare 100MB proxy body limit because they go directly to `*.r2.cloudflarestorage.com`.
- Anchor acceptance scenario (the real workflow this must serve): upload a prepared film session of 3 m4a tracks (~70MB each) + 1 srt via plain curl; see it in the catalog; download every file via presigned GET and verify sha256; then publish a better dub as revision 2 by uploading exactly one new m4a.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new/modified functions and methods
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `gofmt -s`, `golangci-lint run`, `go test ./... -race` after each task

## Code-Quality Rules (verify before marking each task complete)

Materialized verbatim from the `go` skill's Hard rules; these are the gate for marking any task complete. If a rule is violated the task is not done - refactor, re-test, then mark complete.

**Signatures:**
- No function or method has 4+ parameters; `ctx context.Context` does not count. Past the budget, use an options struct (`type fooOpts struct { ... }`).
- No function or method has 4+ return values; split into single-purpose functions or return a struct.
- Adjacent same-type parameters (`oldLine, newLine int`) are a swap hazard - put them on a struct.

**Methods vs standalone helpers:**
- If a function is called only from methods of a single struct, it MUST be a method on that struct. Calling pattern decides, not field access.
- Standalone helpers are only for: constructors/entry points (`New...`, `Parse...`, `Decorate...`), utilities shared by multiple unrelated types, and tiny cross-cutting helpers.
- Before adding a standalone helper, walk its callers; if every caller is a method of one type, make it a method.

**Visibility (private by default):**
- Lowercase identifiers by default; export only when an out-of-package caller exists.
- Exception: a method called by other structs in the same package may be exported for inter-component API clarity - methods only, not types, functions, constants, or variables.
- Before exporting a new identifier, grep for cross-package callers; if none, lowercase it.

**Comments (default: none):**
- Default to no comments; add one only when the WHY is non-obvious (a hidden invariant, a workaround, surprising behavior).
- Exported items get godoc comments starting with the name; unexported get a lowercase comment or none.
- Never describe WHAT self-evident code does; no multi-paragraph comments on routine helpers.

**Per-task gate (before marking a checkbox `[x]`):**
1. `gofmt -s`/`goimports` clean, `golangci-lint run` zero issues, `go test ./... -race` passes.
2. Grep new code for the rules above: `grep -nE '^func.*\(.*,.*,.*,.*\)'` for 4+ params (excluding `ctx`); for each new standalone helper confirm a non-method caller; for each new exported identifier confirm a cross-package caller.
3. Only after 1-2 pass: mark complete.

## Testing Strategy

- **Unit tests**: required for every task; table-driven with `testify/assert`/`testify/require`; one `foo_test.go` per `foo.go`, same package.
- `internal/store` tests run against a real SQLite file in `t.TempDir()`.
- `internal/api` tests use `httptest` with `moq`-generated mocks (stored in `mocks/` subdirectory) for the store and blob interfaces.
- `internal/blob` presign tests run offline - AWS SDK presigners produce signed URLs without network I/O.
- No e2e/UI tests in this repo; the end-to-end acceptance is the manual smoke checklist in the README (Post-Completion).
- Target 80%+ coverage.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Solution Overview

- Single Go binary, stdlib `net/http` (1.22+ method-pattern mux), `log/slog` for structured logs. No web framework.
- Catalog state in SQLite (`modernc.org/sqlite`, WAL mode): one table, manifest stored as a JSON column.
- File bytes never touch the service: `POST /uploads` issues presigned PUT URLs for missing objects; `GET /sessions/{id}` embeds presigned GET URLs. R2 is accessed via `aws-sdk-go-v2` S3 client with a custom endpoint (presign + HeadObject only).
- Two static bearer tokens from env: read token (read endpoints), admin token (everything). Constant-time comparison.
- Composition root in `cmd/allspeak-catalog` wires concrete types through consumer-side interfaces defined in `internal/api`.
- Ships as a scratch-based Docker image, deployed to the droplet via compose + Traefik labels + Spot.

## Technical Details

### R2 object key scheme

`sessions/<sessionID>/files/<sha256>-<sanitizedFilename>` - content-addressed; publishing a revision never overwrites a live key, and identical content is deduplicated per session.

### Manifest JSON shape (wire and storage format)

```json
{
  "tracks": [{"label": "ft.sidon", "sortOrder": 0, "isDefault": true,
              "filename": "film.m4a", "size": 73400320, "sha256": "<64 hex>"}],
  "subtitle": {"filename": "film.srt", "size": 152000, "sha256": "<64 hex>"}
}
```

### Validation rules (finalize endpoints)

- `title`: non-empty, ≤200 chars after trimming.
- `tracks`: ≥1; each `label` non-empty; exactly one track with `isDefault=true`; `sortOrder` any int.
- `subtitle`: required, exactly one.
- Every file entry: `sha256` exactly 64 lowercase hex chars; `size` > 0; `filename` non-empty after sanitization.
- Filename sanitization: take the last path component, keep only `[A-Za-z0-9._ -]`, replace others with `_`; reject if empty afterward.
- Sanitization boundary (single point of truth): filenames are sanitized exactly once, at the API boundary - `/uploads` and both finalize endpoints replace each incoming `filename` with its sanitized form before any key computation, presigning, or storage, and `/uploads` echoes the sanitized names back in its response. Stored manifests therefore always contain sanitized filenames, and every key computation uses the stored/echoed filename verbatim - read paths never re-sanitize.
- Violations → `400` with a JSON body naming the offending field.

### API contract (all under `/api/v1`, JSON)

| Method/path | Auth | Behavior |
|---|---|---|
| `GET /health` | none | `200 {"status":"ok"}` (outside `/api/v1`) |
| `GET /api/v1/catalog` | read | List: `id, title, revision, updatedAt, totalSize, trackLabels[]` per session |
| `GET /api/v1/sessions/{id}` | read | Full manifest; every file entry additionally carries `url` (presigned GET, 1h expiry) and the response carries `urlsExpireAt` |
| `POST /api/v1/uploads` | admin | Body: `{"files":[{"sha256","size","filename"}]}` scoped to `{"sessionId": "<uuid or null>"}`; for new sessions the server allocates and returns `sessionId`. Response per file: `{"sha256","exists":true}` or `{"sha256","uploadUrl"}` (presigned PUT, 1h expiry). Never touches the catalog table |
| `POST /api/v1/sessions` | admin | Finalize new session: `{"sessionId","title","manifest"}` → verify every referenced object exists in R2 (HeadObject); on success insert with `revision=1`, return `{"id","revision"}` |
| `PUT /api/v1/sessions/{id}` | admin | Finalize new revision: same body minus `sessionId`; `404` if unknown id; verify objects; on success increment `revision` atomically, return `{"id","revision"}` |
| `DELETE /api/v1/sessions/{id}` | admin | Remove catalog row (R2 objects left in place - see Non-goals); `204` |

Error semantics: `401` missing/unknown token; `403` read token on admin endpoint; `404` unknown session id; `409` finalize with missing objects, body `{"missing":["<sha256>", ...]}`; `400` validation failure. Presign expiry is a package constant (1 hour), not configurable.

Note on `POST /uploads` and session ids: the object key embeds the session id, so the id must exist before the first upload. Allocating it in `/uploads` (a pure UUID generation, no DB row) keeps finalize atomic while letting keys be scoped per session.

### SQLite schema

`sessions(id TEXT PRIMARY KEY, title TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, manifest TEXT NOT NULL)` - timestamps RFC3339 UTC; WAL mode enabled at open.

### Configuration (env, fail-fast on missing)

| Var | Meaning |
|---|---|
| `AUTH_ADMIN_TOKEN` / `AUTH_READ_TOKEN` | static bearer tokens |
| `CF_ACCESS_KEY_ID` / `CF_ACCESS_SECRET` | R2 credentials |
| `CF_ENDPOINT` | R2 S3 endpoint (`https://<account>.r2.cloudflarestorage.com`) |
| `CF_BUCKET` | bucket name |
| `DB_PATH` | SQLite file path (default `/data/catalog.db`) |
| `LISTEN_ADDR` | default `:8080` |
| `DOMAIN` | compose-level only, used by the Traefik labels |

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): everything achievable in this repo - Go code, tests, Dockerfile, compose, CI, Spot files, README.
- **Post-Completion** (no checkboxes): GitHub repo/ghcr setup, R2 bucket + credentials, DNS record, `.env` on the droplet, first deploy, manual smoke test with a real film session.

## Implementation Steps

### Task 1: Scaffold repository

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `Makefile`, `README.md`

- [x] `go mod init github.com/pkarpovich/allspeak-catalog`, Go 1.25
- [x] `.gitignore`: binaries, `.env`, `*.db`, coverage artifacts
- [x] `.golangci.yml` with the standard linter set used by the `go` skill conventions
- [x] `Makefile` targets: `lint`, `test` (with `-race`), `build`
- [x] `README.md` skeleton: one-paragraph purpose + placeholder sections (API, Config, Deploy, Smoke checklist)
- [x] run `golangci-lint run` on the empty module - clean baseline (`golangci-lint config verify` passes with zero findings; v2.12.2 exits non-zero on a source-less module only because there are no `.go` files yet, which clears in Task 2)

### Task 2: Manifest types and validation (`internal/manifest`)

**Files:**
- Create: `internal/manifest/manifest.go`, `internal/manifest/manifest_test.go`

- [x] types `Manifest`, `Track`, `FileRef` matching the wire shape in Technical Details (JSON tags camelCase)
- [x] `func (m Manifest) Validate() error` implementing every rule from "Validation rules"; errors name the offending field
- [x] `func SanitizeFilename(name string) (string, error)` per the sanitization rule
- [x] `func (m Manifest) Files() []FileRef` - flat list (tracks + subtitle) for iteration by api/blob callers
- [x] write table-driven tests for Validate: valid manifest, each individual rule violation
- [x] write tests for SanitizeFilename: path components, forbidden chars, empty result
- [x] run tests - must pass before task 3

### Task 3: Catalog store (`internal/store`)

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`

- [x] `func New(path string) (*Store, error)` - opens SQLite via `modernc.org/sqlite`, enables WAL, creates the `sessions` table per the schema (idempotent)
- [x] `Session` struct: `ID, Title, Revision, CreatedAt, UpdatedAt`, `Manifest manifest.Manifest`
- [x] methods on `*Store` (all `ctx`-first): `Create` (insert with revision=1; id supplied by caller), `UpdateManifest` (bump revision +1, replace title+manifest, 404-style sentinel error if id unknown), `Get`, `List` (ordered by `created_at` desc), `Delete`
- [x] sentinel `ErrNotFound` for Get/UpdateManifest/Delete on unknown id (plus `ErrExists` for duplicate Create id, needed by the finalize 409 path in Task 8)
- [x] write tests on `t.TempDir()` SQLite: create/get round-trip incl. manifest JSON fidelity, revision increments, list ordering, delete, ErrNotFound cases, reopen-existing-file (idempotent schema)
- [x] run tests - must pass before task 4

### Task 4: R2 blob access (`internal/blob`)

**Files:**
- Create: `internal/blob/blob.go`, `internal/blob/blob_test.go`

- [ ] `Config` struct (endpoint, key id, secret, bucket) + `func New(cfg Config) (*Client, error)`
- [ ] SDK wiring pinned for R2: `config.LoadDefaultConfig` with `config.WithRegion("auto")` and static credentials; endpoint via `s3.NewFromConfig(awsCfg, func(o *s3.Options) { o.BaseEndpoint = ... })` - not the deprecated endpoint-resolver API
- [ ] disable default payload checksums: set `RequestChecksumCalculation` and `ResponseChecksumValidation` to `when_required` - otherwise (SDK >= v1.73, Jan 2025) presigned PUTs sign `x-amz-checksum-crc32` and a plain `curl -T` upload against R2 fails with a signature error
- [ ] `func Key(sessionID, sha256, filename string) string` → `sessions/<id>/files/<sha256>-<filename>` (expects pre-sanitized filename); parameters go on a small struct per the signature rules if they exceed the budget
- [ ] methods: `PresignPut(ctx, key) (string, error)`, `PresignGet(ctx, key) (string, error)` - both with the 1h package constant expiry; `Exists(ctx, key) (bool, error)` via HeadObject mapping NotFound → `(false, nil)`
- [ ] write tests: Key formatting; presign methods produce URLs containing bucket, key, and expiry params (offline - no network)
- [ ] write test: the presigned PUT carries no `x-amz-checksum-*` signed header or query parameter (guards the `when_required` setting)
- [ ] write test: Exists error mapping via an injected HTTP stub or interface seam
- [ ] run tests - must pass before task 5

### Task 5: HTTP server core - auth, health, logging (`internal/api`)

**Files:**
- Create: `internal/api/server.go`, `internal/api/auth.go`, `internal/api/server_test.go`, `internal/api/auth_test.go`

- [ ] `Config` struct (admin token, read token, store iface, blob iface, logger) + `func NewServer(cfg Config) *Server` returning a `*Server` exposing `http.Handler`
- [ ] consumer-side interfaces in this package: `sessionStore` (Create/UpdateManifest/Get/List/Delete) and `objectStore` (PresignPut/PresignGet/Exists) - defined here, satisfied by `internal/store` / `internal/blob`
- [ ] auth middleware: bearer token, `crypto/subtle` comparison; read endpoints accept read or admin token; admin endpoints admin only; `401` vs `403` per the contract
- [ ] request-logging middleware (slog: method, path, status, duration) and `GET /health` without auth
- [ ] `go:generate` moq directives for both interfaces, mocks in `internal/api/mocks/`
- [ ] write httptest tests: health without token; each auth outcome (missing, wrong, read-on-admin, admin-on-read) table-driven
- [ ] run tests - must pass before task 6

### Task 6: Read endpoints - catalog list and session detail

**Files:**
- Create: `internal/api/read.go`, `internal/api/read_test.go`
- Modify: `internal/api/server.go` (route registration)

- [ ] `GET /api/v1/catalog`: map store.List to items `{id, title, revision, updatedAt, totalSize, trackLabels}` (totalSize = sum of manifest file sizes)
- [ ] `GET /api/v1/sessions/{id}`: store.Get + one PresignGet per manifest file; keys are built from the stored (already sanitized) filenames verbatim per the Sanitization boundary; response embeds `url` per file and top-level `urlsExpireAt`; `404` on ErrNotFound
- [ ] write tests with moq mocks: list mapping incl. totalSize/labels, detail URL embedding, presign key uses the stored filename as-is, 404, presign failure → `502`
- [ ] run tests - must pass before task 7

### Task 7: Upload negotiation endpoint

**Files:**
- Create: `internal/api/uploads.go`, `internal/api/uploads_test.go`
- Modify: `internal/api/server.go` (route registration)

- [ ] `POST /api/v1/uploads`: body `{sessionId?, files[]}`; allocate a new UUID when `sessionId` absent; validate each file entry (sha256/size/filename rules from `internal/manifest`); sanitize each filename at this boundary (see Sanitization boundary) and echo the sanitized names in the response; for each file call Exists → respond `exists:true` or `uploadUrl` from PresignPut; echo `sessionId`
- [ ] no catalog/store access in this handler - blob only (keeps half-uploads invisible)
- [ ] write tests: new-session id allocation, existing-session pass-through, mixed exists/missing response, raw filename echoed back sanitized with its key using the sanitized form, validation `400`, blob failure `502`
- [ ] run tests - must pass before task 8

### Task 8: Finalize and delete endpoints

**Files:**
- Create: `internal/api/finalize.go`, `internal/api/finalize_test.go`
- Modify: `internal/api/server.go` (route registration)

- [ ] shared verify step: sanitize incoming manifest filenames at the boundary (see Sanitization boundary), then for every manifest file blob.Exists on its key; collect misses → `409 {"missing":[...]}`; the manifest is stored with the sanitized filenames
- [ ] `POST /api/v1/sessions`: validate manifest, verify objects, store.Create with the client-supplied `sessionId` (must be a valid UUID; `400` otherwise), return `{"id","revision":1}`; duplicate id → `409`
- [ ] `PUT /api/v1/sessions/{id}`: validate, `404` on unknown id, verify objects, store.UpdateManifest, return bumped revision
- [ ] `DELETE /api/v1/sessions/{id}`: store.Delete, `204`; `404` on unknown id
- [ ] write tests: happy create, happy revision bump, 409 with exact missing list, 404s, invalid manifest 400, duplicate create 409, finalize with a raw filename stores the sanitized form and verifies under the sanitized key
- [ ] run tests - must pass before task 9

### Task 9: Composition root (`cmd/allspeak-catalog`)

**Files:**
- Create: `cmd/allspeak-catalog/main.go`, `cmd/allspeak-catalog/config.go`, `cmd/allspeak-catalog/config_test.go`

- [ ] `config.go`: struct with all env vars from Technical Details, `func loadConfig() (config, error)` - fail fast listing every missing required var; defaults for `DB_PATH`, `LISTEN_ADDR`
- [ ] `main.go`: slog JSON logger to stdout, construct store + blob + api.NewServer, `http.Server` with sane timeouts, graceful shutdown on SIGTERM/SIGINT (context with deadline)
- [ ] only this package knows concrete types (composition-root rule)
- [ ] write tests for loadConfig: full env, each missing required var named in the error, defaults applied
- [ ] run tests - must pass before task 10

### Task 10: Dockerfile and compose

**Files:**
- Create: `Dockerfile`, `compose.yaml`, `.env.example`

- [ ] multi-stage Dockerfile: `golang:1.25` build (CGO_ENABLED=0) → `scratch` with CA certs and the binary; container listens on 8080
- [ ] `compose.yaml`: service `allspeak-catalog`, `image: ghcr.io/pkarpovich/allspeak-catalog:latest`, `restart: unless-stopped`, volume `./data:/data`, env passthrough for all vars, external network `proxy`, Traefik labels per the droplet conventions in Context (router rule ``Host(`${DOMAIN}`)``, entrypoint `web-secure`, explicit `loadbalancer.server.port=8080`)
- [ ] `.env.example` with every variable and a comment line each
- [ ] verify (deterministic, no docker daemon needed): grep assertions pass - `Dockerfile` contains `FROM golang:1.25`, `CGO_ENABLED=0`, `FROM scratch`, `ca-certificates`; `compose.yaml` contains `image: ghcr.io/pkarpovich/allspeak-catalog:latest`, the external `proxy` network, and `loadbalancer.server.port=8080` (real `docker build` runs in CI and on the droplet - Post-Completion)
- [ ] run full test suite - must pass before task 11

### Task 11: CI and deploy tooling

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `spot.yml`, `inventory.yml`
- Modify: `Makefile`

- [ ] `ci.yml`: on PR and push to main - `golangci-lint run` + `go test ./... -race`
- [ ] `release.yml`: on push to main - docker build and push `ghcr.io/pkarpovich/allspeak-catalog` with `latest` + git-sha tags (`docker/build-push-action`, `GITHUB_TOKEN` permissions for packages)
- [ ] `spot.yml`: a single task named `deploy` - clone `git@github.com:pkarpovich/allspeak-catalog.git` to `~/allspeak-catalog` if missing, `git pull`, `docker compose pull`, `docker compose up -d`
- [ ] `inventory.yml` with the droplet host (`lasso`); Makefile: `SSH_KEY ?= $(HOME)/.ssh/id_ed25519` and `deploy_%: spot -t $* -v -i ./inventory.yml -k $(SSH_KEY)` (operator overrides `SSH_KEY` via env)
- [ ] verify (deterministic, no external tools): `ci.yml` contains `pull_request` and `push` triggers for `main`, a `golangci-lint` step, and `go test ./... -race`; `release.yml` contains `docker/build-push-action` and `ghcr.io/pkarpovich/allspeak-catalog`; `spot.yml` defines exactly the `deploy` task with the four steps above (real workflow validation happens on the first push - Post-Completion)
- [ ] run full test suite - must pass before task 12

### Task 12: Verify acceptance criteria

- [ ] every endpoint from the API contract table exists with the specified auth, status codes, and shapes (walk the table against the router and tests)
- [ ] every validation rule from Technical Details has a covering test
- [ ] every Non-goal is still a non-goal (no scope creep: no extra endpoints, no config knobs beyond the table)
- [ ] run full test suite: `make test` - green, `-race` clean
- [ ] `golangci-lint run` - zero issues; coverage ≥80%

### Task 13: Update documentation

- [ ] README: purpose, API contract table, env-var table, local run instructions, deploy runbook (Spot), and the manual smoke checklist mirroring the anchor acceptance scenario (upload 3-track film via curl → catalog → presigned download + sha256 check → one-track revision 2)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**GitHub / registry:**
- Create the `pkarpovich/allspeak-catalog` GitHub repo, push, confirm the release workflow publishes to ghcr; make sure the droplet's existing ghcr credentials can pull the package (or set package visibility accordingly). The release workflow performs the first real `docker build` - a broken Dockerfile surfaces here (the in-plan checks are grep-level only).

**Cloudflare:**
- Create the R2 bucket and an R2 API token (S3-compatible key pair); note the account endpoint URL.
- Add the DNS record for the chosen API subdomain (proxied, as with the other droplet services).

**Droplet:**
- Fill `~/allspeak-catalog/.env` on the droplet: `DOMAIN`, both auth tokens (`openssl rand -hex 32` each), `CF_*` R2 values, `DB_PATH=/data/catalog.db`.
- First deploy via `make deploy_deploy` (override `SSH_KEY` env if the key path differs); verify `https://<domain>/health` returns ok and Traefik picked up the router.

**Manual smoke (anchor scenario):**
- Follow the README smoke checklist with a real prepared film session (3 m4a tracks ~70MB + srt): negotiate uploads, PUT files to R2 via curl, finalize, list catalog, download via presigned GETs, compare sha256; then publish revision 2 by adding one new track and confirm only that file needed uploading.

**Follow-up (separate plans):**
- iOS side: catalog screen, background downloads, import via the existing `importMultiTrackSession`, sidecar `server.json` with `{serverID, revision}` - planned in the Allspeak app repo.
- Removal of ShazamKit catalog / DTW-map machinery from the iOS app - separate cleanup plan.

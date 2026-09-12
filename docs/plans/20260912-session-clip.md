# Optional clip in the session manifest

## Overview

Add an optional `clip` file to a session's manifest: a short video cut around the film's first
spoken line, published next to the audio tracks and the subtitle. The Allspeak app downloads it at
import and plays it at home before the cinema, so Pavel sees what happens on screen right before the
first line and knows what to watch for.

The clip is one more content-addressed file. It rides the existing upload flow (`POST /uploads`,
`PUT` bytes to R2, `POST /sessions` or `PUT /sessions/{id}`), is presigned in the detail response
like every other file, and counts toward `totalSize`. Sessions without a clip do not change on the
wire: the key is absent from both stored manifests and responses.

**Non-goals**
- No clip timestamp or label metadata. There is exactly one clip per session and it always covers
  the first line; the app already knows that line's time from the subtitle file.
- No arrays of clips, no "sync points" mid-film.
- No file-type or extension validation. Tracks and subtitles have none either; the clip is cut by
  our own agent.
- No `hasClip` flag in the catalog list. The app learns about the clip from the detail response at
  import, and about a later-added clip from the revision bump that every manifest update already
  produces.
- No R2 cleanup of a replaced clip. Objects are content-addressed and never deleted today; replacing
  a clip leaves the old object like replacing a track does.
- No schema migration. The manifest is a JSON blob in SQLite.

**Rejected alternatives**
- `Clip *FileRef` with `omitempty`: explicit presence, but nil checks in four places and a style that
  differs from the neighbouring `Subtitle` zero-value check. Rejected for a value field with
  `omitzero` (Go 1.24+; go.mod is 1.25).
- `clips []Clip` with `startTime`: allows more markers later, but needs a picker in the app and
  metadata the first-line case does not need. Rejected; revisit only if a second marker is ever
  wanted.
- Streaming the clip from a presigned URL at view time instead of downloading at import: rejected on
  the app side, so nothing on the server changes for it; the server already presigns.

## Skills to invoke

Load each skill below with the Skill tool and follow its conventions before implementing any task in
this plan.

- `go` - signature / visibility / structure / comment conventions for all Go code in this service

## Context (from discovery)

- `internal/manifest/manifest.go`: `FileRef` (filename, size, sha256), `Track` (FileRef + label,
  sortOrder, isDefault), `Manifest` (tracks, subtitle). `Validate()` checks the subtitle by comparing
  to the zero value (`m.Subtitle == (FileRef{})`) and wraps its error as `subtitle.<field>: ...`.
  `Sanitize()` is the single filename-sanitization point. `Files()` flattens tracks then subtitle and
  is the list used by finalize verification, presigning, and `totalSize`.
- `internal/api/read.go`: `catalogItem` (list row, `TotalSize` summed over `Files()`),
  `fileWithURL` (FileRef + url), `sessionDetail`, and `sessionDetail()` which presigns each file with
  `presignFile(ctx, session.ID, FileRef)`.
- `internal/api/finalize.go`: `decodeFinalize` runs `Sanitize()` then `Validate()`; `verifyObjects`
  checks every entry of `Files()` exists in R2 via `blob.Key(sessionID, sha256, filename)`.
- `internal/store/store.go`: manifest stored as JSON text in the `sessions` table; no per-file
  columns.
- Tests: testify, table-driven; helpers `validManifest()`, `track()`, `subtitle()` in
  `manifest_test.go`; `serverWith`, `readGet`, `sampleSession()` in `read_test.go`; `adminReq`,
  `sampleManifest()`, `finalizeBody`, `existsAll()` in `finalize_test.go`; mocks in
  `internal/api/mocks`.
- README documents the manifest shape, validation rules, and a step-by-step publish walkthrough with
  finalize and new-revision examples.
- Commands: `mise run test` (`go test ./... -race`), `mise run lint` (`golangci-lint run`).

## Development Approach

- **testing approach**: Regular - code first, then tests, within the same task
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility: manifests and responses without a clip are byte-for-byte unchanged

## Code-Quality Rules (verify before marking each task complete)

### Go (from the `go` skill, Hard rules)

Non-negotiable; the gate for marking any task complete. If a rule is violated the task is not done - refactor, re-test, then mark complete.

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
- Exception (per CLAUDE.md): a method called by other structs in the same package may be exported for inter-component API clarity - methods only, not types, functions, constants, or variables.
- Before exporting a new identifier, grep for cross-package callers; if none, lowercase it.

**Comments (default: none):**
- Default to no comments; add one only when the WHY is non-obvious (a hidden invariant, a workaround, surprising behavior).
- Being exported is NOT a reason to comment. The visibility rule above exports methods for inter-component clarity, so "godoc on everything exported" turns every export into a comment and fills a package with restated field lists. A godoc comment is for an identifier with a caller in another package AND something to say that the name does not; it starts with the name.
- Never describe WHAT self-evident code does; no multi-paragraph comments on routine helpers.

**Per-task gate (before marking a checkbox `[x]`):**
1. `gofmt -s`/`goimports` clean, `golangci-lint run` zero issues, `go test ./... -race` passes.
2. Grep new code for the rules above: `grep -nE '^func.*\(.*,.*,.*,.*\)'` for 4+ params (excluding `ctx`); for each new standalone helper confirm a non-method caller; for each new exported identifier confirm a cross-package caller.
3. Only after 1-2 pass: mark complete.

## Testing Strategy

- **unit tests**: required for every task; table-driven with testify, extending the existing
  helpers rather than adding parallel ones
- **e2e tests**: none in this project; the acceptance scenario is verified manually against a local
  `mise run dev` instance (see Task 4)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

`Manifest` gains a value field `Clip FileRef` tagged `json:"clip,omitzero"`. Absence is the zero
value, checked exactly the way `Subtitle` presence is checked today. `Validate()`, `Sanitize()`, and
`Files()` each get one branch for a non-zero clip. Because `Files()` feeds finalize verification,
presigning, and `totalSize`, those three behaviours pick up the clip with no further code.

`sessionDetail` gains `Clip fileWithURL` tagged `json:"clip,omitzero"`, filled by
`sessionDetail()` through the same `presignFile` call the subtitle uses. `catalogItem` is unchanged.

`omitzero` drops the key when the struct is all zero values, so stored manifests and API responses
for sessions without a clip do not change.

## Technical Details

**Manifest (storage and finalize request), with a clip:**

```json
{
  "tracks": [ ... ],
  "subtitle": {"filename": "film.srt", "size": 152000, "sha256": "<64 hex>"},
  "clip": {"filename": "film.first-line.mp4", "size": 6200000, "sha256": "<64 hex>"}
}
```

Without a clip the `clip` key is absent. A present clip must pass `FileRef.Validate()` (64 lowercase
hex sha256, size > 0, filename survives sanitization); its error is wrapped as `clip.<field>: ...`
to match `subtitle.<field>: ...`.

**Detail response** (`GET /api/v1/sessions/{id}`): `clip` carries the same three fields plus `url`
(presigned GET), exactly like `subtitle`; absent when the session has no clip. `urlsExpireAt` covers
it.

**List response**: unchanged shape; `totalSize` now includes the clip size when present.

**Ordering in `Files()`**: tracks, subtitle, clip. Finalize's `missing` list and presigning iterate
in that order.

**R2 key**: `blob.Key(sessionID, sha256, filename)` as for every file; no new key scheme.

## Implementation Steps

### Task 1: Clip field in the manifest

**Files:**
- Modify: `internal/manifest/manifest.go`
- Modify: `internal/manifest/manifest_test.go`

- [x] add `Clip FileRef` with tag `json:"clip,omitzero"` to `Manifest`, after `Subtitle`
- [x] in `Validate()`, after the subtitle checks: when `m.Clip != (FileRef{})`, run `m.Clip.Validate()`
      and wrap the error with the `clip.` prefix (same shape as the `subtitle.` wrap)
- [x] in `Sanitize()`: when the clip is non-zero, sanitize `m.Clip.Filename` and wrap the error as
      `clip.filename: ...`
- [x] in `Files()`: append `m.Clip` after the subtitle when non-zero; grow the initial capacity
      accordingly
- [x] add a `clip()` helper in `manifest_test.go` next to `subtitle()` (`film.first-line.mp4`, a
      new sha constant) and a `validManifestWithClip()` built from `validManifest()`
- [x] extend `TestManifestValidate` cases: valid with clip; clip with bad sha256 fails with an
      error containing `clip.sha256`; clip with size 0 fails with `clip.size`; clip with an empty
      filename after sanitization fails with `clip.filename`
- [x] extend the JSON round-trip test: a manifest with a clip survives marshal/unmarshal; marshalling
      a manifest without a clip produces JSON that does not contain the substring `"clip"`
- [x] extend the `Sanitize` test: a clip filename with a path and forbidden characters is reduced to
      its sanitized base name; a manifest without a clip stays without one after `Sanitize()`
- [x] extend the `Files()` test: with a clip the list is tracks, subtitle, clip (length +1); without
      a clip the list is unchanged
- [x] run `mise run test` and `mise run lint` - must pass before task 2
      (⚠️ the `mise` shim is broken in this sandbox - no `.env` and no go@1.25 shim for
      golangci-lint; ran the underlying `go test ./... -race` and
      `/mise/installs/go/1.23.12/bin/golangci-lint run` instead: all pass, 0 issues)

### Task 2: Clip in the session detail response

**Files:**
- Modify: `internal/api/read.go`
- Modify: `internal/api/read_test.go`

- [x] add `Clip fileWithURL` with tag `json:"clip,omitzero"` to `sessionDetail`, after `Subtitle`
- [x] in `sessionDetail()`: when `session.Manifest.Clip` is non-zero, presign it with `presignFile`
      and set `Clip`; a presign failure returns the error like the subtitle path does
      (extracted as a `presignClip` method on `Server` to keep `sessionDetail()` flat)
- [x] extend `sampleSession()` usage: add a `sampleSessionWithClip()` (or a clip-bearing variant)
      in `read_test.go` so both shapes are available to tests
- [x] test `GET /api/v1/sessions/{id}` with a clip: response `clip` has filename, size, sha256 and a
      `url` produced by the object-store mock for `blob.Key(id, sha256, filename)`
- [x] test `GET /api/v1/sessions/{id}` without a clip: the raw response body does not contain the
      substring `"clip"`
- [x] test presign failure on the clip returns `502` like an existing presign-failure test
- [x] test the catalog list: `totalSize` for a session with a clip equals tracks + subtitle + clip
      sizes; `trackLabels` unchanged
- [x] run `mise run test` and `mise run lint` - must pass before task 3
      (⚠️ same broken `mise` shim as task 1; ran `go test ./... -race` and
      `/mise/installs/go/1.23.12/bin/golangci-lint run` instead: all pass, 0 issues)

### Task 3: Finalize accepts and verifies the clip

No production code is expected to change: `decodeFinalize` already sanitizes and validates the
whole manifest, and `verifyObjects` already walks `Files()`. This task proves it with tests and
fixes anything that turns out not to hold.

**Files:**
- Modify: `internal/api/finalize_test.go`

- [x] add a clip-bearing manifest builder next to `sampleManifest()`
- [x] test `POST /api/v1/sessions` with a clip whose object exists (`existsAll()`): `200`, revision 1,
      and the stored session's manifest (captured through the session-store mock) contains the clip
      with its sanitized filename
- [x] test `POST /api/v1/sessions` with a clip whose object is missing: same status and body shape as
      the existing missing-track case, with the clip's sha256 in `missing`
- [x] test `POST /api/v1/sessions` with an invalid clip (bad sha256): `400` with a message starting
      `manifest.clip.sha256` (table also covers `clip.size` and `clip.filename`)
- [x] test `PUT /api/v1/sessions/{id}` with a manifest that adds a clip to an existing session:
      `200` and the revision returned by the store mock; and with a manifest that drops the clip:
      `200`, no error (dropping is just a manifest without the key)
- [x] run `mise run test` and `mise run lint` - must pass before task 4
      (⚠️ same broken `mise` shim as tasks 1-2; ran `go test ./... -race` and
      `/mise/installs/go/1.23.12/bin/golangci-lint run` instead: all pass, 0 issues.
      No production code changed - `decodeFinalize` and `verifyObjects` already handled the clip)

### Task 4: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented: optional clip in manifest, presigned in
      detail, counted in `totalSize`, verified at finalize, absent key when no clip
- [ ] verify edge cases: clip-only manifest without subtitle still fails on `subtitle: required`;
      zero-value clip after `Sanitize()` stays zero; `omitzero` drops the key on both manifest and
      detail
- [ ] run full test suite: `mise run test`
- [ ] run lint: `mise run lint`
- [ ] manual acceptance against `mise run dev` with a throwaway session id: request upload URLs for
      two small files (a text "subtitle" and a text "clip") and one "track" via `POST /uploads`, PUT
      the bytes, finalize with a manifest containing the clip, then `GET` the detail and confirm
      `clip.url` downloads the same bytes; finalize a second session without a clip and confirm the
      detail JSON has no `clip` key; delete both sessions

### Task 5: Update documentation

**Files:**
- Modify: `README.md`

- [ ] update the intro sentence that describes a session ("one or more audio tracks and exactly one
      subtitle") to mention the optional clip
- [ ] update the manifest shape block to show the optional `clip` entry and the validation-rules
      paragraph to state it is optional and validated like the subtitle when present
- [ ] update the detail-endpoint row to mention `clip` is presigned like every other file when present
- [ ] update the publish walkthrough: the sha256 step and the finalize example include the clip; the
      new-revision example shows adding a clip as one way to bump a revision
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Deployment**
- `mise run deploy` (spot) to roll the new binary to the catalog host; no config or DB changes.

**External system updates**
- `allspeak` iOS app: import and sync must download the clip and store it in the session directory;
  the player gets a button that pauses the dub and shows the clip in a sheet. Separate plan in that
  repo.
- `cinema-prep` skill: add the step that cuts the clip around the first subtitle cue and includes it
  in the publish manifest (`clip` entry alongside `tracks` and `subtitle`).

# Optional fingerprint in the session manifest

## Overview

Add an optional `fingerprint` file to a session's manifest: a ShazamKit custom catalog
(`.shazamcatalog`, about 1 MB) built from the music and effects of the published RU track. The
Allspeak app downloads it at import. In the cinema the iPhone and the Apple Watch match the hall's
sound against it to find where the dub track should be, and Pavel resyncs with one tap.

The fingerprint is one more content-addressed file and follows exactly the path the optional `clip`
took (commits `4e5b70c`, `c719cb9`, plan `docs/plans/completed/20260912-session-clip.md`):
- it rides the existing upload flow (`POST /uploads`, `PUT` bytes to R2, `POST /sessions` or
  `PUT /sessions/{id}`);
- it is presigned in the detail response;
- it counts toward `totalSize`.

Sessions without a fingerprint do not change on the wire: the key is absent from both stored
manifests and responses.

**Non-goals**
- No metadata beside the file. The catalog carries its own chunk offsets inside its media items.
- No file-type or extension validation, same as tracks, subtitle and clip.
- No `hasFingerprint` flag in the catalog list. The app learns about a fingerprint from the detail
  response at import, and about a later-added one from the revision bump.
- No R2 cleanup of a replaced fingerprint.
- No schema migration. The manifest is a JSON blob in SQLite.

**Rejected alternatives**
- `Fingerprint *FileRef` with `omitempty`: rejected for the same reason as the clip. Use a value
  field with `omitzero`, matching `Subtitle` and `Clip`.
- Storing the fingerprint as an extra track or as part of the clip: rejected. Tracks are audio the
  app plays, and the clip is a video. A separate field keeps both meanings clean.

## Skills to invoke

Load each skill below with the Skill tool and follow its conventions before implementing any task in
this plan.

- `go` - signature / visibility / structure / comment conventions for all Go code in this service

## Context (from discovery)

- `internal/manifest/manifest.go`: `FileRef`, `Track`, `Manifest` (tracks, subtitle, clip).
  - `Validate()` checks the clip only when it is non-zero and wraps errors as `clip.<field>: ...`.
  - `Sanitize()` sanitizes a non-zero clip filename.
  - `Files()` returns tracks, then subtitle, then clip. That list feeds finalize verification,
    presigning and `totalSize`.
- `internal/api/read.go`: `sessionDetail` carries `Clip fileWithURL` (`json:"clip,omitzero"`),
  filled through the `presignClip` method on `Server`.
- `internal/api/finalize.go`: `decodeFinalize` runs `Sanitize()` then `Validate()`, and
  `verifyObjects` walks `Files()`.
- Tests: testify, table-driven; existing clip helpers and cases in `manifest_test.go`,
  `read_test.go`, `finalize_test.go`. Mirror each clip case for the fingerprint.
- Commands: `mise run test` (`go test ./... -race`) and `mise run lint` (`golangci-lint run`). If the
  `mise` shim is broken in the sandbox, run the underlying commands, as the clip plan did.

## Development Approach

- **testing approach**: Regular - code first, then tests, within the same task
- complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- maintain backward compatibility: manifests and responses without a fingerprint are byte-for-byte
  unchanged, including sessions that have a clip

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

- **unit tests**: required for every task. Table-driven with testify, extending the existing clip
  helpers rather than adding parallel ones.
- **e2e tests**: none in this project.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview

`Manifest` gains `Fingerprint FileRef` tagged `json:"fingerprint,omitzero"`, after `Clip`.
`Validate()`, `Sanitize()` and `Files()` each get one branch for a non-zero fingerprint, the same as
the clip. Because `Files()` feeds finalize verification, presigning and `totalSize`, those pick it
up automatically.

`sessionDetail` gains `Fingerprint fileWithURL` tagged `json:"fingerprint,omitzero"`, filled the way
the clip is.

## Technical Details

- Manifest with a fingerprint:
  `"fingerprint": {"filename": "film.shazamcatalog", "size": 980000, "sha256": "<64 hex>"}`.
  The key is absent without one.
- Validation errors are wrapped as `fingerprint.<field>: ...`.
- Detail response: same three fields plus `url`, absent when there is no fingerprint.
- List response: unchanged shape; `totalSize` includes the fingerprint size.
- `Files()` order: tracks, subtitle, clip, fingerprint.
- R2 key: `blob.Key(sessionID, sha256, filename)` as for every file.

## Implementation Steps

### Task 1: Fingerprint field in the manifest

**Files:**
- Modify: `internal/manifest/manifest.go`
- Modify: `internal/manifest/manifest_test.go`

- [x] add `Fingerprint FileRef` with tag `json:"fingerprint,omitzero"` to `Manifest`, after `Clip`
- [x] add the non-zero branches:
  - `Validate()`: wrap errors as `fingerprint.`;
  - `Sanitize()`: wrap errors as `fingerprint.filename: ...`;
  - `Files()`: append it after the clip and grow the capacity.
- [x] add a `fingerprint()` test helper next to `clip()`, plus a manifest builder with both clip
      and fingerprint
- [x] extend the validate, round-trip, sanitize and `Files()` tests the way the clip cases do:
  - valid;
  - bad sha256 → `fingerprint.sha256`;
  - size 0 → `fingerprint.size`;
  - empty filename → `fingerprint.filename`;
  - a manifest without a fingerprint marshals with no `"fingerprint"` substring;
  - `Files()` order is tracks, subtitle, clip, fingerprint.
- [x] run `mise run test` and `mise run lint` - must pass before task 2

### Task 2: Fingerprint in the session detail response

**Files:**
- Modify: `internal/api/read.go`
- Modify: `internal/api/read_test.go`

- [x] add `Fingerprint fileWithURL` with tag `json:"fingerprint,omitzero"` to `sessionDetail`,
      after `Clip`
- [x] presign a non-zero fingerprint in `sessionDetail()` through a `presignFingerprint` method on
      `Server`, mirroring `presignClip`. Factor out a shared method only if it keeps every function
      within the Go signature rules.
      (done as a shared `presignOptionalFile(ctx, sessionID, f)` method that replaced `presignClip`
      and serves both the clip and the fingerprint; ctx + 2 params, within the rules)
- [x] write tests:
  - detail with a fingerprint returns filename, size, sha256 and the mock-presigned `url`;
  - detail without one has no `"fingerprint"` substring;
  - presign failure on the fingerprint returns `502`;
  - catalog `totalSize` includes the fingerprint.
- [x] run `mise run test` and `mise run lint` - must pass before task 3

### Task 3: Finalize accepts and verifies the fingerprint

**Files:**
- Modify: `internal/api/finalize_test.go`

- [x] write tests:
  - `POST /api/v1/sessions` with a fingerprint whose object exists stores it with its sanitized
    filename;
  - a missing fingerprint object returns `409`, with its sha256 in `missing`;
  - an invalid fingerprint returns `400`, with a message starting `manifest.fingerprint.`;
  - `PUT /api/v1/sessions/{id}` adding a fingerprint to a session that already has tracks, a
    subtitle and a clip returns `200`, and the stored manifest keeps the other files unchanged.
- [x] fix production code only if one of these does not hold (all held, no production change)
- [x] run `mise run test` and `mise run lint` - must pass before task 4

### Task 4: Verify acceptance criteria

- [x] verify every point:
  - optional fingerprint in the manifest;
  - presigned in the detail response;
  - counted in `totalSize`;
  - verified at finalize;
  - key absent when there is none;
  - sessions with a clip but no fingerprint are unchanged.
- [x] run the full test suite and lint

### Task 5: [Final] Update documentation

- [x] update `README.md`:
  - manifest shape: add the `fingerprint` example;
  - validation rules: the fingerprint is optional and validated like the clip;
  - the publish walkthrough: adding a fingerprint in a new revision.
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

- After the merge, `release.yml` builds `ghcr.io/pkarpovich/allspeak-catalog:latest`; deploy with
  `spot` (task `deploy`).
- Then check that an existing session's detail response is unchanged.
- The Allspeak app gets a matching change separately (decode, download and import the
  fingerprint).

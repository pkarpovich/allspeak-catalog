# allspeak-catalog

A small Go service that distributes prepared Allspeak sessions (film dub audio tracks plus subtitles)
online instead of over AirDrop. It exposes an authenticated JSON API over a catalog of sessions and
keeps file payloads in Cloudflare R2, transferred exclusively through presigned URLs - the service
stays on the JSON control plane and never proxies file bytes. The Mac uploads a session once; the
iOS app imports it from anywhere later.

A session in the catalog is a `title` plus a monotonically increasing `revision` plus a manifest of
files: one or more audio tracks and exactly one subtitle. Content is addressed by sha256 in R2, so
publishing a new revision uploads only new files and interrupted uploads resume by re-requesting
upload URLs.

## API

All endpoints are JSON. Authenticated endpoints take a bearer token: `Authorization: Bearer <token>`.
Two static tokens exist - a read token (read endpoints) and an admin token (everything). Presigned
URLs expire one hour after they are issued.

| Method / path | Auth | Behavior |
|---|---|---|
| `GET /health` | none | `200 {"status":"ok"}` (outside `/api/v1`) |
| `GET /api/v1/catalog` | read | List sessions: `id, title, revision, updatedAt, totalSize, trackLabels[]` per session |
| `GET /api/v1/sessions/{id}` | read | Full manifest; every file entry additionally carries `url` (presigned GET) and the response carries `urlsExpireAt`; `404` if unknown |
| `POST /api/v1/uploads` | admin | Body `{"sessionId": "<uuid or null>", "files":[{"sha256","size","filename"}]}`; for a new session the server allocates and returns `sessionId`. Response per file: `{"sha256","filename","exists":true}` or `{"sha256","filename","uploadUrl"}` (presigned PUT). Never touches the catalog table |
| `POST /api/v1/sessions` | admin | Finalize a new session: `{"sessionId","title","manifest"}`; verifies every referenced object exists in R2, inserts with `revision=1`, returns `{"id","revision"}`. `409` if the id already exists |
| `PUT /api/v1/sessions/{id}` | admin | Finalize a new revision: body same as above minus `sessionId`; `404` if unknown id; verifies objects; increments `revision`; returns `{"id","revision"}` |
| `DELETE /api/v1/sessions/{id}` | admin | Remove the catalog row (R2 objects left in place); `204`; `404` if unknown id |

Error semantics: `401` missing/unknown token; `403` read token on an admin endpoint; `404` unknown
session id; `409` finalize with missing objects (body `{"missing":["<sha256>", ...]}`) or duplicate
create id; `400` validation failure (body names the offending field).

### Manifest shape

The `manifest` in both finalize endpoints, and the storage/wire format for a session, is:

```json
{
  "tracks": [{"label": "ft.sidon", "sortOrder": 0, "isDefault": true,
              "filename": "film.m4a", "size": 73400320, "sha256": "<64 hex>"}],
  "subtitle": {"filename": "film.srt", "size": 152000, "sha256": "<64 hex>"}
}
```

Validation rules: `title` non-empty and at most 200 chars after trimming; at least one track; each
track `label` non-empty; exactly one track with `isDefault=true`; subtitle required; every file
`sha256` exactly 64 lowercase hex chars, `size` > 0, `filename` non-empty after sanitization.
Filenames are sanitized once at the API boundary (last path component, keep `[A-Za-z0-9._ -]`,
replace the rest with `_`), so stored manifests and R2 keys always use the sanitized names.

## Config

All variables are read from the environment. The service fails fast at startup if any required
variable is missing.

| Var | Required | Default | Meaning |
|---|---|---|---|
| `AUTH_ADMIN_TOKEN` | yes | - | Static bearer token for admin endpoints |
| `AUTH_READ_TOKEN` | yes | - | Static bearer token for read endpoints |
| `CF_ACCESS_KEY_ID` | yes | - | R2 access key id |
| `CF_ACCESS_SECRET` | yes | - | R2 secret access key |
| `CF_ENDPOINT` | yes | - | R2 S3 endpoint (`https://<account>.r2.cloudflarestorage.com`) |
| `CF_BUCKET` | yes | - | R2 bucket name |
| `DB_PATH` | no | `/data/catalog.db` | SQLite file path |
| `LISTEN_ADDR` | no | `:8080` | HTTP listen address |
| `DOMAIN` | compose only | - | Public host for the Traefik router labels |

See `.env.example` for a copy-paste template.

## Local run

```sh
make test          # go test ./... -race
make lint          # golangci-lint run
make build         # static binary at ./allspeak-catalog

# run against a local SQLite file and real R2 credentials
export AUTH_ADMIN_TOKEN=... AUTH_READ_TOKEN=...
export CF_ACCESS_KEY_ID=... CF_ACCESS_SECRET=... CF_ENDPOINT=... CF_BUCKET=...
export DB_PATH=./catalog.db LISTEN_ADDR=:8080
./allspeak-catalog
```

`GET http://localhost:8080/health` should return `{"status":"ok"}`.

## Deploy

The service runs on the droplet as a git checkout with `compose.yaml` at the repo root, behind the
shared Traefik gateway (external docker network `proxy`, TLS terminated at the edge). Images are
published to `ghcr.io/pkarpovich/allspeak-catalog` by the release workflow on push to `main`.

Deploys use [Spot](https://github.com/umputun/spot). The `deploy` task clones the repo if missing,
`git pull`s, `docker compose pull`s the latest image, and `docker compose up -d`. The target host is
in `inventory.yml` (`lasso`).

```sh
# from a machine with ssh access to the droplet
make deploy_deploy                              # uses ~/.ssh/id_ed25519
make deploy_deploy SSH_KEY=/path/to/other/key   # override the key
```

On the droplet, `~/allspeak-catalog/.env` must contain `DOMAIN`, both auth tokens, the four `CF_*`
R2 values, and `DB_PATH=/data/catalog.db`. After a deploy, `https://<domain>/health` should return
`{"status":"ok"}` and Traefik should show the router.

## Smoke checklist

End-to-end check with a real prepared film session (3 m4a tracks of ~70MB plus one srt), matching the
workflow the catalog exists to serve. Set `API` and `ADMIN` first, then work through the steps.

```sh
export API=https://<domain>/api/v1
export ADMIN="Authorization: Bearer <admin-token>"
export READ="Authorization: Bearer <read-token>"
```

1. Compute size and sha256 for every file (macOS): `stat -f%z track1.m4a` and
   `shasum -a 256 track1.m4a`. Do this for the three tracks and the subtitle.

2. Negotiate uploads (no `sessionId` yet, so the server allocates one):

   ```sh
   curl -s -H "$ADMIN" -H 'Content-Type: application/json' "$API/uploads" -d '{
     "sessionId": null,
     "files": [
       {"sha256":"<t1>","size":73400320,"filename":"track1.m4a"},
       {"sha256":"<t2>","size":71000000,"filename":"track2.m4a"},
       {"sha256":"<t3>","size":72000000,"filename":"track3.m4a"},
       {"sha256":"<sub>","size":152000,"filename":"film.srt"}
     ]
   }'
   ```

   The response carries the allocated `sessionId` and, per file, either `"exists":true` or an
   `"uploadUrl"`. Note the `sessionId`.

3. PUT each file's bytes directly to R2 using its `uploadUrl` (a plain upload, no auth header - the
   URL is presigned):

   ```sh
   curl -s -X PUT --upload-file track1.m4a "<uploadUrl-for-track1>"
   ```

   Repeat for every file that came back with an `uploadUrl`.

4. Finalize the session (revision 1). `sessionId` is the one from step 2; the manifest uses the same
   sha256/size/filename values:

   ```sh
   curl -s -H "$ADMIN" -H 'Content-Type: application/json' "$API/sessions" -d '{
     "sessionId": "<sessionId>",
     "title": "My Film",
     "manifest": {
       "tracks": [
         {"label":"original","sortOrder":0,"isDefault":true,"filename":"track1.m4a","size":73400320,"sha256":"<t1>"},
         {"label":"alt-1","sortOrder":1,"isDefault":false,"filename":"track2.m4a","size":71000000,"sha256":"<t2>"},
         {"label":"alt-2","sortOrder":2,"isDefault":false,"filename":"track3.m4a","size":72000000,"sha256":"<t3>"}
       ],
       "subtitle": {"filename":"film.srt","size":152000,"sha256":"<sub>"}
     }
   }'
   ```

   Expect `{"id":"<sessionId>","revision":1}`.

5. List the catalog and confirm the session appears with the right `revision`, `totalSize`, and
   `trackLabels`:

   ```sh
   curl -s -H "$READ" "$API/catalog"
   ```

6. Fetch the detail, download every file via its presigned `url`, and verify the sha256 matches:

   ```sh
   curl -s -H "$READ" "$API/sessions/<sessionId>"
   curl -s "<url-for-track1>" -o out1.m4a && shasum -a 256 out1.m4a   # must equal <t1>
   ```

7. Publish a better dub as revision 2 by uploading exactly one new track. Negotiate uploads again
   with the existing `sessionId`; only the new track should return an `uploadUrl` (the unchanged
   files report `"exists":true`). PUT the new track, then finalize with `PUT`:

   ```sh
   curl -s -X PUT -H "$ADMIN" -H 'Content-Type: application/json' "$API/sessions/<sessionId>" -d '{
     "title": "My Film",
     "manifest": { "tracks": [ ... revised ... ], "subtitle": { ... } }
   }'
   ```

   Expect `{"id":"<sessionId>","revision":2}`, and confirm the catalog now shows `revision:2`.

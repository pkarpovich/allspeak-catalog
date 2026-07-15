# allspeak-catalog

A small Go service that distributes prepared Allspeak sessions (film dub audio tracks plus subtitles)
online instead of over AirDrop. It exposes an authenticated JSON API over a catalog of sessions and
keeps file payloads in Cloudflare R2, transferred exclusively through presigned URLs - the service
stays on the JSON control plane and never proxies file bytes. The Mac uploads a session once; the
iOS app imports it from anywhere later.

## API

_TODO: API contract table (endpoints, auth, status codes)._

## Config

_TODO: environment variable table._

## Deploy

_TODO: Spot deploy runbook for the droplet._

## Smoke checklist

_TODO: manual end-to-end smoke test with a real film session._

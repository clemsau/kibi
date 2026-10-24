# Kibi

Kibi is a lightweight, multi-cluster Elasticsearch workbench for teams that use
Elasticsearch primarily as an application search engine. One Kibi server stores
a catalogue of clusters; users sign in to each cluster with their own native
Elasticsearch credentials.

## Vertical slice

The initial implementation supports:

- Two local Elasticsearch 8 clusters (development and production).
- Server-side credential pass-through with memory-only per-cluster sessions.
- Cluster, node, index, shard, mapping, settings, aliases, and storage inspection.
- An index workspace with a scoped document browser and raw mapping/settings views.
- Query DSL search with both expandable documents and a complete raw JSON response.
- Guarded single-document create/replace and delete, including production
  confirmation and session audit events.
- A focused REST console with cluster/search/node examples. It accepts only
  Elasticsearch paths on the selected cluster and requires `PRODUCTION` for
  mutating requests against a production-labelled cluster.

`make up` also seeds the development cluster with `products`, `customers`,
`orders`, and `support_tickets`, including mappings, an alias, and realistic
sample documents. The production cluster intentionally remains empty.

To add or restore that sample data on an already-running development cluster,
run `make seed`. The operation is idempotent for the bundled document IDs.

Kibi is not an identity provider. Elasticsearch always decides whether the
currently signed-in user may perform a request.

## Screenshots

### Cluster overview

One workbench can surface health, index and shard counts, connected user,
Elasticsearch compatibility details, and quick paths into operational views.

![Kibi cluster overview](docs/screenshots/overview.jpg)

### Index workspace

Open an index to inspect its mapping, settings, aliases, statistics, and
documents with a scoped Query DSL browser.

![Kibi index workspace](docs/screenshots/index-workspace.jpg)

### Search response, your way

Search results can be read as expandable documents or switched to the complete
Elasticsearch JSON response when debugging needs the exact payload.

![Kibi raw search response](docs/screenshots/search-raw-json.jpg)

### REST console

Use ready-made recipes or run a JSON Elasticsearch request directly against the
selected cluster with its native credentials and production safeguards.

![Kibi REST console](docs/screenshots/rest-console.jpg)

## Run locally

Prerequisites: Docker, Go 1.22+, and Node.js 20+.

```sh
make up
make api
make web
```

Open `http://localhost:5173`. The local development cluster uses
`elastic` / `changeme-dev`; the local production cluster uses `elastic` /
`changeme-prod`.

The API listens on `http://localhost:8080` by default. Default clusters are local only;
for a real deployment, pass `KIBI_CLUSTERS` as a JSON array using the shape in
[config/clusters.example.json](config/clusters.example.json). The Kibi server
must be able to reach those Elasticsearch endpoints over the private network or
VPN.

Run `make build` to compile the backend and frontend.

On some macOS Go installations, the default linker produces a binary that the
system loader rejects. `make api` uses Go's external linker to avoid that issue.

The complete product direction is in
[docs/product-direction.md](docs/product-direction.md).

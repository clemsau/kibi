# Kibi website

This directory is a zero-build static product website, separate from the Kibi
workbench.

## Deploy with the existing Cloudflare Worker

The current Kibi URL is a Workers deployment (workers.dev), not a Pages
project. The bundled wrangler.jsonc config serves this directory as static
Worker assets.

In Workers & Pages, open the Kibi Worker, then open Settings → Builds and use:

| Setting | Value |
| --- | --- |
| Production branch | main |
| Root directory | website |
| Build command | leave empty |
| Deploy command | npx wrangler deploy |
| Preview command | npx wrangler preview |

Save the settings, then retry the latest deployment or push a new commit to
main. The Worker will upload index.html, styles.css, and assets from this
directory. Future main commits deploy to production automatically; non-main
branches can create Worker previews.

## Deploy with Cloudflare Pages instead

If you later create a Pages project rather than use the existing Worker, use:

| Setting | Value |
| --- | --- |
| Framework preset | None |
| Build command | exit 0 |
| Build output directory | website |
| Root directory | leave empty |

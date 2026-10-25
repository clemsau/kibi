# Kibi website

This directory is a zero-build static product website, separate from the Kibi
workbench.

## Cloudflare Pages

Connect the clemsau/kibi repository in Workers & Pages, select the main branch,
and use:

- Framework preset: None
- Build command: leave empty
- Build output directory: website
- Root directory: leave empty

Cloudflare Pages will serve website/index.html. Every commit pushed to main
will update the production site, while pull requests can receive previews.

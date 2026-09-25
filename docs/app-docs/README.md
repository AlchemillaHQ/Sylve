# Sylve App Docs

Astro + Starlight docs site for Sylve.

## Commands

| Command | Action |
| :-- | :-- |
| `npm install` | Install dependencies |
| `npm run dev` | Start local dev server |
| `npm run build` | Build site |
| `npm run preview` | Preview production build |

## Search

Site search uses Starlight's default [Pagefind](https://pagefind.app/) setup.
Astro builds the index into `dist/pagefind/` and the search UI loads it from
there at runtime.

`pagefind` is pinned to **1.5.0** in `package.json`. Starting with 1.5.1, the
indexer binary aborts on hosts with memory pages larger than 4 KiB
(`<jemalloc>: Unsupported system page size`, for example aarch64 Linux on
Asahi), and the failure happens after Astro reports a successful build, so a
deployment could ship without any search index. The pin is a stopgap until the
upstream fix ships (tracked for Pagefind 1.6.x in
[issue #1147](https://github.com/Pagefind/pagefind/issues/1147)).

Because that failure is silent, `npm run build` runs
`scripts/check-search-index.mjs` after `astro build` and fails when
`dist/pagefind/pagefind.js` is missing.

// Fails the build when the site would ship without a search index.
//
// Pagefind's indexer is a native binary that can abort without failing the
// Astro build (see https://github.com/Pagefind/pagefind/issues/1147). Astro
// still reports success in that case, so `wrangler deploy` would happily
// upload a site whose search box returns nothing. Checking after `astro build`
// turns that silent failure into a visible one.
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const indexFile = path.join(root, 'dist', 'pagefind', 'pagefind.js');

if (!existsSync(indexFile)) {
    console.error(
        '[search] Pagefind did not produce dist/pagefind/pagefind.js.\n' +
            '[search] Deploying now would ship a search box that returns no results.\n' +
            '[search] Check that the pinned Pagefind version in package.json is still installed,\n' +
            '[search] or build the site on a host with 4 KiB memory pages.'
    );
    process.exit(1);
}

console.log('[search] Pagefind index present at dist/pagefind/pagefind.js');

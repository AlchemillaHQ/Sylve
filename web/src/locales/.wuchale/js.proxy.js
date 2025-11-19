
            /** @type {{[loadID: string]: {[locale: string]: () => Promise<import('wuchale/runtime').CatalogModule>}}} */
            const catalogs = {js: {en: () => import('./main.main.en.compiled.js'),mal: () => import('./main.main.mal.compiled.js'),hi: () => import('./main.main.hi.compiled.js')}}
            export const loadCatalog = (/** @type {string} */ loadID, /** @type {string} */ locale) => catalogs[loadID][locale]()
            export const loadIDs = ['js']
        
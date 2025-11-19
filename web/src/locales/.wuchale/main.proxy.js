
            /** @type {{[loadID: string]: {[locale: string]: () => Promise<import('wuchale/runtime').CatalogModule>}}} */
            const catalogs = {main: {en: () => import('./main.main.en.compiled.js'),mal: () => import('./main.main.mal.compiled.js')}}
            export const loadCatalog = (/** @type {string} */ loadID, /** @type {string} */ locale) => catalogs[loadID][locale]()
            export const loadIDs = ['main']
        
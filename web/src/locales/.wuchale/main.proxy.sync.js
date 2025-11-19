
            import * as _w_c_main_0_ from './main.main.en.compiled.js'
import * as _w_c_main_1_ from './main.main.mal.compiled.js'
            /** @type {{[loadID: string]: {[locale: string]: import('wuchale/runtime').CatalogModule}}} */
            const catalogs = {main: {en: _w_c_main_0_,mal: _w_c_main_1_}}
            export const loadCatalog = (/** @type {string} */ loadID, /** @type {string} */ locale) => catalogs[loadID][locale]
            export const loadIDs = ['main']
        
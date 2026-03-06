import { loadCatalog, loadIDs } from './.wuchale/js.proxy.js'
import { registerLoaders } from 'wuchale/load-utils'

const key = 'js'

// two exports. can be used anywhere
export const getRuntime = registerLoaders(key, loadCatalog, loadIDs)
export const getRuntimeRx = getRuntime

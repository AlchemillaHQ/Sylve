import { getDownloads } from '$lib/api/utilities/downloader';
import { getVmById } from '$lib/api/vm/vm';
import { getVMDomain, getVMs } from '$lib/api/vm/vm';
import { getDatasets } from '$lib/api/zfs/datasets';
import { getPools } from '$lib/api/zfs/pool';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';
import { SEVEN_DAYS } from '$lib/utils.js';
import { cachedFetch } from '$lib/utils/http';

export async function load({ params }) {
    const cacheDuration = SEVEN_DAYS;
    const rid = params.node;

    const [vms, vm, domain, filesystems, volumes, pools, downloads] = await Promise.all([
        cachedFetch('vms', async () => await getVMs(), cacheDuration),
        cachedFetch(`vm-${rid}`, async () => await getVmById(Number(rid), 'rid'), cacheDuration),
        cachedFetch(`vm-domain-${rid}`, async () => getVMDomain(Number(rid)), cacheDuration),
        cachedFetch('zfs-filesystems', async () => await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM), cacheDuration),
        cachedFetch(
            'zfs-volumes',
            async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
            cacheDuration
        ),
        cachedFetch('pools', async () => getPools(), cacheDuration),
        cachedFetch('download-list', async () => getDownloads(), cacheDuration)
    ]);

    return {
        vms: vms,
        vm: vm,
        rid: rid,
        domain: domain,
        filesystems: filesystems,
        volumes: volumes,
        pools: pools,
        downloads: downloads
    };
}

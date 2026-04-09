import { getTargets } from '$lib/api/iscsi/target';
import { getDatasets } from '$lib/api/zfs/datasets';
import { SEVEN_DAYS } from '$lib/utils';
import { cachedFetch } from '$lib/utils/http';
import { GZFSDatasetTypeSchema } from '$lib/types/zfs/dataset';

export async function load() {
    const cacheDuration = SEVEN_DAYS;
    const [targets, volumes] = await Promise.all([
        cachedFetch('iscsi-targets', async () => await getTargets(), cacheDuration),
        cachedFetch(
            'zfs-volumes',
            async () => await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME),
            cacheDuration
        )
    ]);

    return {
        targets,
        volumes
    };
}

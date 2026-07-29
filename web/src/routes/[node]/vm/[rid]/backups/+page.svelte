<script lang="ts">
	import BackupJobsPage from '$lib/components/custom/DataCenter/Backups/Jobs/Page.svelte';
	import type { BackupJob, BackupTarget } from '$lib/types/cluster/backups';
	import type { ClusterNode } from '$lib/types/cluster/cluster';

	let {
		data
	}: {
		data: {
			rid: number;
			hostname: string;
			targets: BackupTarget[];
			jobs: BackupJob[];
			nodes: ClusterNode[];
			localNodeId: string;
			standaloneMode: boolean;
		};
	} = $props();
</script>

<BackupJobsPage {data} vmRid={data.rid} vmHostname={data.hostname} />

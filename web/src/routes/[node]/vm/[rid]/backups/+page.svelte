<script lang="ts">
	import BackupJobsPage from '$lib/components/custom/DataCenter/Backups/Jobs/Page.svelte';
	import type { BackupJobsPageData } from '$lib/utils/backup-jobs-page-state';

	let {
		data
	}: {
		data: BackupJobsPageData & {
			rid: number;
			hostname: string;
		};
	} = $props();
</script>

<BackupJobsPage {data} vmRid={data.rid} vmHostname={data.hostname} />

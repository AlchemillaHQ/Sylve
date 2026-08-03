<script lang="ts">
	import BackupJobsPage from '$lib/components/custom/DataCenter/Backups/Jobs/Page.svelte';
	import type { BackupGuestScope } from '$lib/types/cluster/backups';
	import type { BackupJobsPageData } from '$lib/utils/backup-jobs-page-state';

	let {
		data
	}: {
		data: BackupJobsPageData & {
			guestScope: BackupGuestScope;
		};
	} = $props();
</script>

{#key `${data.guestScope.kind}:${data.guestScope.id}:${data.guestScope.hostname}`}
	<BackupJobsPage {data} guestScope={data.guestScope} />
{/key}

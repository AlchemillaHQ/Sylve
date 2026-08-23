<script lang="ts">
	import BackupJobsPage from '$lib/components/custom/DataCenter/Backups/Jobs/Page.svelte';
	import type { BackupJobsPageData } from '$lib/utils/backup-jobs-page-state';

	let { data }: { data: BackupJobsPageData } = $props();
</script>

<BackupJobsPage {data} />

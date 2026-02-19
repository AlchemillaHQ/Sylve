<script lang="ts">
	import { listBackupJobs } from '$lib/api/cluster/backups';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { storage } from '$lib';
	import type { BackupJob } from '$lib/types/cluster/backups';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { sha256 } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';
	import { getJails } from '$lib/api/jail/jail';

	let filterJobId = $state('');
	let reload = $state(false);
	let hash = $state('');

	let jobs = resource(
		() => 'backup-jobs-for-filter',
		async () => {
			const res = await listBackupJobs();
			return res;
		},
		{ initialValue: [] as BackupJob[] }
	);

	let jails = $state<any[]>([]);
	let jailsLoading = $state(false);

	async function loadJails() {
		if (jails.length > 0 || jailsLoading) return;
		jailsLoading = true;
		try {
			const res = await getJails();
			updateCache('jail-list', res);
			jails = res;
			if (hash) {
				reload = true;
			}
		} finally {
			jailsLoading = false;
		}
	}

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
		loadJails();
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);

	let eventColumns = $derived.by((): Column[] => {
		const currentJails = jails;

		return [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'status',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					switch (value) {
						case 'success':
							return renderWithIcon('mdi:check-circle-outline', 'Success', 'text-green-500');
						case 'failed':
							return renderWithIcon('mdi:close-circle-outline', 'Failed', 'text-red-500');
						case 'running':
							return renderWithIcon('mdi:progress-clock', 'Running', 'text-yellow-500');
						default:
							return value || '-';
					}
				}
			},
			{
				field: 'sourceDataset',
				title: 'Source',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (!value || currentJails.length === 0) return value || '';

					if (value.includes('/sylve/jails/')) {
						const jail = currentJails.find((j: any) => {
							const baseStorage = j.storages?.find((s: any) => s.isBase);
							if (baseStorage) {
								const jailDataset = `${baseStorage.pool}/sylve/jails/${j.ctId}`;
								return jailDataset === value;
							}
							return false;
						});
						if (jail) {
							return renderWithIcon('hugeicons:prison', jail.name);
						}
					}

					return renderWithIcon('material-symbols:files', value);
				}
			},
			{ field: 'targetEndpoint', title: 'Target' },
			{ field: 'mode', title: 'Mode' },
			{
				field: 'startedAt',
				title: 'Started',
				formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
			},
			{
				field: 'completedAt',
				title: 'Completed',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return value ? convertDbTime(value) : '-';
				}
			},
			{
				field: 'error',
				title: 'Error',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return value ? `<span class="text-red-500">${value}</span>` : '-';
				}
			}
		];
	});

	let tableData = $derived({
		rows: [],
		columns: eventColumns
	});

	let extraParams = $derived.by((): Record<string, string | number> => {
		if (filterJobId) {
			return { jobId: parseInt(filterJobId) };
		}
		return {};
	});

	let jobOptions = $derived([
		{ value: '', label: 'All Jobs' },
		...jobs.current.map((job) => ({
			value: String(job.id),
			label: job.name
		}))
	]);

	// 			{#key `${jails.length}-${filterJobId}`}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<div class="w-48">
			<SimpleSelect
				placeholder="Filter by job"
				options={jobOptions}
				bind:value={filterJobId}
				onChange={() => (reload = true)}
				classes={{
					parent: 'w-full',
					trigger: '!h-6.5 text-sm'
				}}
			/>
		</div>

		<Button onclick={() => (reload = true)} size="sm" variant="outline" class="ml-auto h-6">
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] h-4 w-4"></span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		{#if hash && jailsLoading === false}
			{#key `${jails.length}-${filterJobId}`}
				<TreeTable
					data={tableData}
					name="backup-events-tt"
					ajaxURL="/api/cluster/backups/events/remote?hash={hash}"
					bind:query
					bind:parentActiveRow={activeRows}
					bind:reload
					multipleSelect={false}
					{extraParams}
					initialSort={[{ column: 'startedAt', dir: 'desc' }]}
				/>
			{/key}
		{/if}
	</div>
</div>

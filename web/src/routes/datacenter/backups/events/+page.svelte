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
	import { resource } from 'runed';
	import { onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';
	import { renderWithIcon } from '$lib/utils/table';

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

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);

	const eventColumns: Column[] = [
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'direction',
			title: 'Direction',
			formatter: (cell: CellComponent) => {
				const value = cell.getValue();
				let v = { name: '', icon: '' };

				if (value === 'send') {
					v = { name: 'Send', icon: 'mdi:upload' };
				} else if (value === 'receive') {
					v = { name: 'Receive', icon: 'mdi:download' };
				}

				return renderWithIcon(v.icon, v.name);
			}
		},
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
		{ field: 'sourceDataset', title: 'Source Dataset' },
		{ field: 'destinationDataset', title: 'Destination Dataset' },
		{ field: 'baseSnapshot', title: 'Base Snapshot' },
		{ field: 'targetSnapshot', title: 'Target Snapshot' },
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
		{#if hash}
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
		{/if}
	</div>
</div>

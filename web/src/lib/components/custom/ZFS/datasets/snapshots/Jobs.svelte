<script lang="ts">
	import { deletePeriodicSnapshot, getDatasets } from '$lib/api/zfs/datasets';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table';
	import { GZFSDatasetTypeSchema, type PeriodicSnapshot } from '$lib/types/zfs/dataset';
	import { handleAPIError } from '$lib/utils/http';
	import { cronToHuman, dateToAgo } from '$lib/utils/time';
	import { getDatasetByGUID } from '$lib/utils/zfs/dataset/dataset';
	import { toast } from 'svelte-sonner';
	import Retention from './Retention.svelte';
	import { resource } from 'runed';

	interface Data {
		open: boolean;
		periodicSnapshots: PeriodicSnapshot[];
		reload: boolean;
	}

	let { open = $bindable(), periodicSnapshots, reload = $bindable() }: Data = $props();

	let datasets = resource(
		() => 'zfs-fs-vol-datasets',
		async () => {
			const fs = await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM);
			const vol = await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME);
			return [...fs, ...vol];
		},
		{
			initialValue: []
		}
	);

	let shadowDeleted: number[] = $state([]);

	function getDatasetName(guid: string) {
		const dataset = getDatasetByGUID(datasets.current, guid);
		if (dataset) {
			return dataset.name;
		}
		return '';
	}

	function intervalToString(interval: number) {
		switch (interval) {
			case 0:
				return 'None';
			case 60:
				return 'Every Minute';
			case 3600:
				return 'Every Hour';
			case 86400:
				return 'Every Day';
			case 604800:
				return 'Every Week';
			case 2419200:
				return 'Every Month';
			case 29030400:
				return 'Every Year';
			default:
				return `${interval} seconds`;
		}
	}

	async function saveJobs() {
		try {
			for (const id of shadowDeleted) {
				const snapshot = periodicSnapshots.find((s) => s.id === id);
				if (snapshot) {
					const response = await deletePeriodicSnapshot(snapshot.guid);
					reload = true;
					if (response.error) {
						handleAPIError(response);
						toast.error('Failed to delete periodic snapshot', {
							position: 'bottom-center'
						});
					} else {
						open = false;
					}
				}
			}
		} catch (e) {
			console.error('Error saving snapshot jobs:', e);
		}
	}

	let retention = $state({
		open: false,
		snapshot: null as PeriodicSnapshot | null,
		dataset: ''
	});
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
		class="fixed top-1/2 left-1/2 w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-hidden p-0 lg:max-w-3xl"
	>
		<div class="flex items-center justify-between p-6">
			<Dialog.Header class="p-0">
				<Dialog.Title>
					<div class="flex items-center gap-2">
						<span class="icon-[material-symbols--save-clock] h-5 w-5"></span>
						<span>View Snapshot Jobs</span>
					</div>
				</Dialog.Title>
			</Dialog.Header>
		</div>

		<div class="max-h-75 overflow-y-auto px-6" id="table-body">
			<Table.Root>
				<Table.Header class="bg-background sticky top-0 z-10">
					<Table.Row>
						<Table.Head class="w-2.5 pl-6">ID</Table.Head>
						<Table.Head class="w-50">Dataset</Table.Head>
						<Table.Head class="w-50">Prefix</Table.Head>
						<Table.Head class="w-50">Interval</Table.Head>
						<Table.Head class="w-50">Last Run</Table.Head>
						<Table.Head class="w-50"></Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#if periodicSnapshots && periodicSnapshots.length > 0}
						{#each periodicSnapshots as snapshot (snapshot.guid)}
							<Table.Row class="h-10">
								<Table.Cell class="pl-6">{snapshot.id}</Table.Cell>
								<Table.Cell>{getDatasetName(snapshot.guid)}</Table.Cell>
								<Table.Cell>{snapshot.prefix}</Table.Cell>

								{#if snapshot.interval !== 0}
									<Table.Cell>{intervalToString(snapshot.interval)}</Table.Cell>
								{:else if snapshot.cronExpr}
									<Table.Cell>{cronToHuman(snapshot.cronExpr)}</Table.Cell>
								{/if}

								<Table.Cell title={snapshot.lastRunAt.toLocaleString()}>
									{@const lastRun = dateToAgo(snapshot.lastRunAt)}
									{#if lastRun === 'Jan 01, 0001'}
										<span>Never</span>
									{:else}
										{lastRun}
									{/if}
								</Table.Cell>

								{#if !shadowDeleted.includes(snapshot.id)}
									<Table.Cell style="padding-block: 0.25rem">
										<Button
											variant="ghost"
											class="h-8"
											onclick={() => {
												const snap = periodicSnapshots.find((s) => s.id === snapshot.id) || null;
												retention = {
													open: true,
													snapshot: snap,
													dataset: getDatasetName(snap?.guid || '')
												};
											}}
										>
											<span class="icon-[lucide--timer-reset] h-4 w-4"></span>
										</Button>
										<Button
											variant="ghost"
											class="h-8"
											onclick={() => shadowDeleted.push(snapshot.id)}
										>
											<span class="icon-[gg--trash] h-4 w-4"></span>
										</Button>
									</Table.Cell>
								{:else}
									<Table.Cell class="py-1">
										<span class="text-muted-foreground text-xs italic">Deleted</span>
									</Table.Cell>
								{/if}
							</Table.Row>
						{/each}
					{:else}
						<Table.Row>
							<Table.Cell colspan={6} class="text-muted-foreground h-20 text-center">
								No snapshot jobs
							</Table.Cell>
						</Table.Row>
					{/if}
				</Table.Body>
			</Table.Root>
		</div>

		<Dialog.Footer class="flex justify-between gap-2 border-t px-6 py-4">
			<div class="flex gap-2">
				{#if shadowDeleted.length > 0}
					<Button size="sm" onclick={saveJobs}>Save Snapshot Jobs</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

{#if retention.open}
	<Retention
		bind:open={retention.open}
		snapshot={retention.snapshot}
		dataset={retention.dataset}
		bind:reload
	/>
{/if}

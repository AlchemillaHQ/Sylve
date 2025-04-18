<script lang="ts">
	import { createVolume, deleteVolume, getDatasets } from '$lib/api/zfs/datasets';
	import { getPools } from '$lib/api/zfs/pool';
	import AlertDialogModal from '$lib/components/custom/AlertDialog.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Input from '$lib/components/ui/input/input.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import * as Select from '$lib/components/ui/select/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Dataset, GroupedByPool } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import { isValidSize } from '$lib/utils/numbers';
	import { generatePassword } from '$lib/utils/string';
	import { isValidPoolName } from '$lib/utils/zfs';
	import { groupByPool } from '$lib/utils/zfs/dataset/dataset';
	import { createVolProps, generateTableData } from '$lib/utils/zfs/dataset/volume';
	import Icon from '@iconify/svelte';
	import { useQueries } from '@sveltestack/svelte-query';
	import toast from 'svelte-french-toast';

	interface Data {
		pools: Zpool[];
		datasets: Dataset[];
	}

	let { data }: { data: Data } = $props();
	let tableName = 'tt-zfsVolumes';

	const results = useQueries([
		{
			queryKey: ['poolList'],
			queryFn: async () => {
				return await getPools();
			},
			refetchInterval: 1000,
			keepPreviousData: false,
			initialData: data.pools
		},
		{
			queryKey: ['datasetList'],
			queryFn: async () => {
				return await getDatasets();
			},
			refetchInterval: 1000,
			keepPreviousData: false,
			initialData: data.datasets
		}
	]);

	let grouped: GroupedByPool[] = $derived(groupByPool($results[0].data, $results[1].data));
	let table: {
		rows: Row[];
		columns: Column[];
	} = $derived(generateTableData(grouped));
	let activeRow: Row | null = $state(null);
	let activePool: Zpool | null = $derived.by(() => {
		const pool = $results[0].data?.find((pool) => pool.name === activeRow?.name);
		return pool ?? null;
	});

	let activeVolume: Dataset | null = $derived.by(() => {
		const volume = $results[1].data?.find(
			(volume) => volume.name === activeRow?.name && volume.type === 'volume'
		);
		return volume ?? null;
	});

	type props = {
		checksum: string;
		compression: string;
		dedup: string;
		encryption: string;
		volblocksize: string;
	};

	let confirmModals = $state({
		active: '' as 'createVolume' | 'deleteVolume',
		createVolume: {
			open: false,
			data: {
				name: '',
				properties: {
					parent: '',
					checksum: 'on',
					compression: 'on',
					dedup: 'off',
					encryption: 'off',
					encryptionKey: '',
					volblocksize: '16384',
					size: ''
				}
			},
			title: ''
		},
		deleteVolume: {
			open: false,
			data: '',
			title: ''
		}
	});

	let zfsProperties = $state(createVolProps);

	async function confirmAction() {
		if (confirmModals.active === 'createVolume') {
			if (!isValidPoolName(confirmModals.createVolume.data.name)) {
				toast.error('Invalid name', {
					position: 'bottom-center'
				});
				return;
			}

			if (!confirmModals.createVolume.data.properties.parent) {
				toast.error('No parent selected', {
					position: 'bottom-center'
				});
				return;
			}

			if (confirmModals.createVolume.data.properties.encryption !== 'off') {
				if (confirmModals.createVolume.data.properties.encryptionKey === '') {
					toast.error('Encryption key is required', {
						position: 'bottom-center'
					});
					return;
				}
			}

			if (!isValidSize(confirmModals.createVolume.data.properties.size)) {
				toast.error('Invalid size', {
					position: 'bottom-center'
				});
				return;
			}

			console.log(
				await createVolume(
					confirmModals.createVolume.data.name,
					confirmModals.createVolume.data.properties.parent,
					confirmModals.createVolume.data.properties
				)
			);
		}

		if (confirmModals.active === 'deleteVolume') {
			if (activeVolume) {
				console.log(await deleteVolume(activeVolume));
			}
		}
	}
</script>

{#snippet button(type: string)}
	{#if type === 'delete-volume' && activeVolume?.type === 'volume'}
		<Button
			on:click={async () => {
				if (activeRow) {
					confirmModals.active = 'deleteVolume';
					confirmModals.deleteVolume.open = true;
					confirmModals.deleteVolume.data = activeRow.name;
					confirmModals.deleteVolume.title = activeRow.name;
				}
			}}
			size="sm"
			class="bg-muted-foreground/40 dark:bg-muted h-6 text-black disabled:!pointer-events-auto disabled:hover:bg-neutral-600 dark:text-white"
		>
			<Icon icon="mdi:delete" class="mr-1 h-4 w-4" /> Delete Volume
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			on:click={() => {
				confirmModals.active = 'createVolume';
				confirmModals.createVolume.open = true;
				confirmModals.createVolume.title = '';
			}}
			size="sm"
			class="h-6"
		>
			<Icon icon="gg:add" class="mr-1 h-4 w-4" /> New
		</Button>

		{@render button('delete-volume')}
	</div>
	<div class="relative flex h-full w-full cursor-pointer flex-col">
		<div class="flex-1">
			<div class="h-full overflow-y-auto">
				<TreeTable data={table} name={tableName} bind:parentActiveRow={activeRow} />
			</div>
		</div>
	</div>
</div>

{#snippet simpleSlect(prop: keyof props, label: string, placeholder: string)}
	<div class="space-y-1">
		<Label class="w-24 whitespace-nowrap text-sm">{label}</Label>
		<Select.Root
			selected={{
				label:
					zfsProperties[prop].find(
						(option) => option.value === confirmModals.createVolume.data.properties[prop]
					)?.label || confirmModals.createVolume.data.properties[prop],
				value: confirmModals.createVolume.data.properties[prop]
			}}
			onSelectedChange={(value) => {
				confirmModals.createVolume.data.properties[prop] = value?.value || '';
			}}
		>
			<Select.Trigger class="w-full">
				<Select.Value {placeholder} />
			</Select.Trigger>

			<Select.Content class="max-h-36 overflow-y-auto">
				<Select.Group>
					{#each zfsProperties[prop] as option}
						<Select.Item value={option.value} label={option.label}>{option.label}</Select.Item>
					{/each}
				</Select.Group>
			</Select.Content>
		</Select.Root>
	</div>
{/snippet}

{#if confirmModals.active === 'createVolume'}
	<Dialog.Root
		bind:open={confirmModals.createVolume.open}
		closeOnOutsideClick={false}
		closeOnEscape={false}
	>
		<Dialog.Content
			class="fixed left-1/2 top-1/2 max-h-[90vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform gap-0 overflow-visible overflow-y-auto p-0 transition-all duration-300 ease-in-out lg:max-w-[70%]"
		>
			<div class="flex items-center justify-between">
				<Dialog.Header class="flex justify-between p-4">
					<Dialog.Title class="flex items-center text-left">
						<Icon icon="carbon:volume-block-storage" class="mr-2 h-5 w-5" />Create Volume</Dialog.Title
					>
				</Dialog.Header>
				<Dialog.Close
					class="ring-offset-background data-[state=open]:bg-accent data-[state=open]:text-muted-foreground mr-4 flex h-5 w-5 items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100 focus:outline-none focus:ring-0 disabled:pointer-events-none"
				>
					<Icon icon="lucide:x" class="h-5 w-5" />
					<span class="sr-only">Close</span>
				</Dialog.Close>
			</div>

			<div class="w-full p-4">
				<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
					<div class="space-y-1">
						<Label for="name">Name</Label>
						<Input
							type="text"
							id="name"
							placeholder="volume"
							autocomplete="off"
							bind:value={confirmModals.createVolume.data.name}
						/>
					</div>

					<div class="space-y-1">
						<Label class="w-24 whitespace-nowrap text-sm">Size</Label>
						<Input
							type="text"
							class="w-full text-left"
							min="0"
							bind:value={confirmModals.createVolume.data.properties.size}
							placeholder="128M"
						/>
					</div>

					<div class="space-y-1">
						<Label class="w-24 whitespace-nowrap text-sm">Parent</Label>
						<Select.Root
							selected={{
								label: confirmModals.createVolume.data.properties.parent || activePool?.name,
								value: confirmModals.createVolume.data.properties.parent || activePool?.name
							}}
							onSelectedChange={(value) => {
								confirmModals.createVolume.data.properties.parent = value?.value || '';
							}}
						>
							<Select.Trigger class="w-full">
								<Select.Value placeholder="Select Parent" />
							</Select.Trigger>

							<Select.Content class="max-h-36 overflow-y-auto">
								<Select.Group>
									{#each grouped as group}
										<Select.Item value={group.pool.name} label={group.pool.name}
											>{group.pool.name}</Select.Item
										>
									{/each}
								</Select.Group>
							</Select.Content>
						</Select.Root>
					</div>

					{@render simpleSlect('volblocksize', 'Block Size', 'Select block size')}
					{@render simpleSlect('checksum', 'Checksum', 'Select checksum algorithm')}
					{@render simpleSlect('compression', 'Compression', 'Select compression type')}
					{@render simpleSlect('dedup', 'Deduplication', 'Select deduplication mode')}
					{@render simpleSlect('encryption', 'Encryption', 'Select encryption')}

					{#if confirmModals.createVolume.data.properties.encryption !== 'off'}
						<div class="space-y-1">
							<Label class="w-24 whitespace-nowrap text-sm">Passphrase</Label>
							<div class="flex w-full max-w-sm items-center space-x-2">
								<Input
									type="password"
									id="d-passphrase"
									placeholder="Enter or generate passphrase"
									class="w-full"
									autocomplete="off"
									bind:value={confirmModals.createVolume.data.properties.encryptionKey}
									showPasswordOnFocus={true}
								/>

								<Button
									onclick={() => {
										confirmModals.createVolume.data.properties.encryptionKey = generatePassword();
									}}
								>
									<Icon
										icon="fad:random-2dice"
										class="h-6 w-6"
										onclick={() => {
											confirmModals.createVolume.data.properties.encryptionKey = generatePassword();
										}}
									/>
								</Button>
							</div>
						</div>
					{/if}
				</div>
			</div>

			<Dialog.Footer>
				<div class="flex items-center justify-end space-x-4 p-4">
					<Button
						size="sm"
						type="button"
						variant="ghost"
						class="disabled border-border h-8 w-full border"
						onclick={() => {
							confirmModals.createVolume.open = false;
						}}
					>
						Cancel
					</Button>
					<Button
						size="sm"
						type="button"
						class="h-8 w-full bg-blue-600 text-white hover:bg-blue-700"
						onclick={() => {
							confirmAction();
						}}
					>
						Create
					</Button>
				</div>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}

{#if confirmModals.active == 'deleteVolume'}
	<AlertDialogModal
		open={confirmModals.active && confirmModals[confirmModals.active].open}
		names={{
			parent: 'volume',
			element: confirmModals.active ? confirmModals[confirmModals.active].title || '' : ''
		}}
		actions={{
			onConfirm: () => {
				if (confirmModals.active) {
					confirmAction();
				}
			},
			onCancel: () => {
				if (confirmModals.active) {
					confirmModals[confirmModals.active].open = false;
				}
			}
		}}
	></AlertDialogModal>
{/if}

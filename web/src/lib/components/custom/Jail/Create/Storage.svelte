<script lang="ts">
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Zpool } from '$lib/types/zfs/pool';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { CreateData } from '$lib/types/jail/jail';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { fstabPlaceholder } from '$lib/utils/placeholders';
	import { toast } from 'svelte-sonner';
	import * as Select from '$lib/components/ui/select/index.js';
	import { generateSimpleLinuxFSTab, resolveJailRootPreview } from '$lib/utils/jail/jail';
	import { watch } from 'runed';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import Bootstrap from './Bootstrap.svelte';

	interface Props {
		hostname?: string;
		ctId: number;
		pools: Zpool[];
		datasets: Dataset[];
		pool: string;
		downloads: Download[];
		bootstraps: BootstrapEntry[];
		bootstrapRefetch: boolean;
		base: string;
		fstab: string;
		source: 'base' | 'zfs';
		zfsSource: CreateData['storage']['zfsSource'];
		sourceDatasets: Dataset[];
		sourcesLoading: boolean;
		sourcesError: string;
	}

	let {
		hostname,
		ctId,
		pools,
		datasets,
		downloads,
		bootstraps,
		bootstrapRefetch = $bindable(),
		pool = $bindable(),
		base = $bindable(),
		fstab = $bindable(),
		source = $bindable(),
		zfsSource = $bindable(),
		sourceDatasets,
		sourcesLoading,
		sourcesError
	}: Props = $props();

	let bootstrapModalOpen = $state(false);
	let sourceOpen = $state(false);
	let datasetOpen = $state(false);
	let datasetOptions = $derived(
		sourceDatasets.map((dataset) => ({ label: dataset.name, value: dataset.name }))
	);
	let sourceMembers = $derived(
		datasets.filter(
			(dataset) =>
				zfsSource.dataset &&
				(dataset.name === zfsSource.dataset || dataset.name.startsWith(zfsSource.dataset + '/'))
		)
	);
	let sourceSize = $derived(sourceMembers.reduce((sum, dataset) => sum + dataset.referenced, 0));

	function selectSource(value: string | string[]) {
		if (value === 'zfs') {
			base = '';
		} else {
			zfsSource = { dataset: '', guid: '' };
		}
	}

	function selectDataset(value: string | string[]) {
		const dataset = sourceDatasets.find((dataset) => dataset.name === value);
		zfsSource = {
			dataset: dataset?.name || '',
			guid: dataset?.guid || ''
		};
	}

	let poolOptions = $derived.by(() => {
		return pools.map((pool) => ({
			label: pool.name,
			value: pool.name
		}));
	});

	let baseOptions = $derived.by(() => {
		const downloadOpts = downloads
			.filter((download) => download.uType === 'base-rootfs' && download.status === 'done')
			.map((download) => ({
				label: download.name,
				value: download.uuid
			}));

		const bootstrapOpts = bootstraps
			.filter((b) => b.exists && b.status === 'completed')
			.map((b) => ({
				label: b.label,
				value: `bootstrap:${b.name}`
			}));

		return [...bootstrapOpts, ...downloadOpts];
	});

	let comboBoxes = $state({
		pool: {
			open: false,
			options: [] as { label: string; value: string }[]
		},
		base: {
			open: false,
			options: [] as { label: string; value: string }[]
		}
	});

	let disableBaseSelection = $derived(pool ? false : true);
	let enableFstabInput = $state(false);
	let jailRoot = $derived(resolveJailRootPreview(datasets, pool, ctId));
	let simpleLinuxAvailable = $derived(jailRoot !== null);
	let fstabMode = $state<'manual' | 'simple-linux'>('manual');

	watch([() => base, () => enableFstabInput], ([baseVal, fstabEnabled]) => {
		if (fstabEnabled && !baseVal && source !== 'zfs') {
			toast.warning('Select a base/rootfs to add FStab entries', {
				position: 'bottom-center'
			});
			enableFstabInput = false;
			fstabMode = 'manual';
			fstab = '';
		} else if (!fstabEnabled) {
			fstabMode = 'manual';
			fstab = '';
		}
	});

	function setFstabMode(value: string) {
		if (value === 'simple-linux') {
			if (!jailRoot) return;
			fstabMode = value;
			fstab = generateSimpleLinuxFSTab(jailRoot);
			return;
		}

		fstabMode = 'manual';
		fstab = '';
	}

	function markFstabManual() {
		fstabMode = 'manual';
	}

	watch([() => jailRoot, () => fstabMode], ([root, mode]) => {
		if (mode !== 'simple-linux') return;
		if (!root) {
			fstabMode = 'manual';
			fstab = '';
			return;
		}

		fstab = generateSimpleLinuxFSTab(root);
	});
</script>

<div class="flex flex-col gap-4 p-4">
	<CustomComboBox
		bind:open={sourceOpen}
		label="Source"
		bind:value={source}
		data={[
			{ label: 'Base / Bootstrap', value: 'base' },
			{ label: 'Existing ZFS', value: 'zfs' }
		]}
		onValueChange={selectSource}
		disallowEmpty
		triggerWidth="w-full"
		width="w-full"
	/>
	<div class="grid grid-cols-2 gap-4">
		<CustomComboBox
			bind:open={comboBoxes.pool.open}
			label="Pool"
			bind:value={pool}
			data={poolOptions}
			classes="flex-1 space-y-1"
			placeholder="Select ZFS pool"
			triggerWidth="w-full"
			width="w-full"
		></CustomComboBox>

		{#if source === 'base'}
			<CustomComboBox
				bind:open={comboBoxes.base.open}
				label="Base"
				bind:value={base}
				data={baseOptions}
				classes="flex-1 space-y-1"
				placeholder="Select base"
				triggerWidth="w-full"
				width="w-full"
				disabled={disableBaseSelection}
				topRightButton={pool
					? {
							icon: 'icon-[mdi--download-box-outline]',
							tooltip: 'Bootstrap a base',
							function: async () => {
								bootstrapModalOpen = true;
								return '';
							}
						}
					: undefined}
			></CustomComboBox>
		{:else}
			<div
				class="min-w-0"
				title={!sourcesLoading && !sourcesError && sourceDatasets.length === 0
					? 'No eligible jail root datasets found. Registered jails, internal datasets, and unsupported roots are excluded.'
					: undefined}
			>
				<CustomComboBox
					bind:open={datasetOpen}
					label="Source Dataset"
					value={zfsSource.dataset}
					data={datasetOptions}
					onValueChange={selectDataset}
					placeholder={sourcesLoading ? 'Loading source datasets…' : 'Select jail root dataset'}
					disabled={sourcesLoading || sourcesError !== '' || datasetOptions.length === 0}
					triggerWidth="w-full"
					width="w-full"
				/>
			</div>
		{/if}
	</div>
	{#if source === 'zfs'}
		{#if sourcesError}
			<p class="text-destructive text-xs" role="alert">{sourcesError}</p>
		{/if}
		{#if sourceMembers.length > 0}<p class="text-muted-foreground text-xs">
				Referenced data estimate: {formatBytesBinary(sourceSize)}. Actual copy size may differ.
			</p>{/if}
	{/if}
	<CustomCheckbox
		label="FStab Additions"
		bind:checked={enableFstabInput}
		classes="flex items-center gap-2"
	></CustomCheckbox>

	{#if enableFstabInput}
		<div>
			<CustomValueInput
				label="FStab Entries"
				placeholder={fstabPlaceholder}
				type="textarea"
				textAreaClasses="min-h-40 text-xs/6"
				bind:value={fstab}
				classes="flex-1 space-y-1 text-xs/6 mb-2"
				onChange={markFstabManual}
			/>

			<Select.Root type="single" value={fstabMode} onValueChange={setFstabMode}>
				<Select.Trigger class="h-8 w-full">
					{fstabMode === 'simple-linux' ? 'Simple Linux' : 'Manual'}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="simple-linux" disabled={!simpleLinuxAvailable}>
						Simple Linux
					</Select.Item>
					<Select.Item value="manual">Manual</Select.Item>
				</Select.Content>
			</Select.Root>
			{#if pool && !simpleLinuxAvailable}
				<p class="text-muted-foreground mt-2 text-xs">
					The Simple Linux preset is unavailable until this pool's managed jails mountpoint can be
					resolved.
				</p>
			{/if}
		</div>
	{/if}
</div>

<Bootstrap
	bind:open={bootstrapModalOpen}
	{pool}
	{hostname}
	onComplete={() => {
		bootstrapRefetch = true;
	}}
/>

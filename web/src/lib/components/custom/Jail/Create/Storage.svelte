<script lang="ts">
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Zpool } from '$lib/types/zfs/pool';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { fstabPlaceholder } from '$lib/utils/placeholders';
	import { toast } from 'svelte-sonner';
	import * as Select from '$lib/components/ui/select/index.js';
	import { generateSimpleLinuxFSTab, resolveJailRootPreview } from '$lib/utils/jail/jail';
	import { watch } from 'runed';
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
		fstab = $bindable()
	}: Props = $props();

	let bootstrapModalOpen = $state(false);

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
		if (fstabEnabled && !baseVal) {
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
	</div>
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

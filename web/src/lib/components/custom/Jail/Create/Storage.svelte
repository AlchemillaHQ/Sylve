<script lang="ts">
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type { Jail } from '$lib/types/jail/jail';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { fstabPlaceholder } from '$lib/utils/placeholders';
	import { toast } from 'svelte-sonner';

	interface Props {
		pools: Zpool[];
		pool: string;
		downloads: Download[];
		base: string;
		jails: Jail[];
		fstab: string;
	}

	let {
		pools,
		downloads,
		pool = $bindable(),
		base = $bindable(),
		fstab = $bindable()
	}: Props = $props();

	let poolOptions = $derived.by(() => {
		return pools.map((pool) => ({
			label: pool.name,
			value: pool.name
		}));
	});

	let baseOptions = $derived.by(() => {
		return downloads
			.filter((download) => download.uType === 'base-rootfs')
			.map((download) => ({
				label: download.name,
				value: download.uuid
			}));
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

	$effect(() => {
		if (!base && enableFstabInput) {
			toast.warning('Please select a base/rootfs before adding FStab entries', {
				position: 'bottom-center'
			});
			enableFstabInput = false;
		}
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
			triggerWidth="w-full "
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
		></CustomComboBox>
	</div>
	<CustomCheckbox
		label="FStab Additions"
		bind:checked={enableFstabInput}
		classes="flex items-center gap-2"
	></CustomCheckbox>

	{#if enableFstabInput}
		<CustomValueInput
			label="FStab Entries"
			placeholder={fstabPlaceholder}
			type="textarea"
			textAreaClasses="min-h-40 text-xs/6"
			bind:value={fstab}
			classes="flex-1 space-y-1 text-xs/6"
		/>
	{/if}
</div>

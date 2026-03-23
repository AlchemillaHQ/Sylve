<script lang="ts">
	import { createJailFromTemplate } from '$lib/api/jail/jail';
	import { getPools } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { isValidVMName } from '$lib/utils/string';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		hostname?: string;
		nextGuestId: number;
	}

	let { open = $bindable(), templateId, templateLabel, hostname, nextGuestId }: Props = $props();

	let createMode = $state<'single' | 'multiple'>('single');

	// svelte-ignore state_referenced_locally
	let singleCTID = $state(nextGuestId || 0);

	// svelte-ignore state_referenced_locally
	let multipleStartCTID = $state(nextGuestId || 0);

	let singleName = $state('');
	let multipleCount = $state(1);
	let multipleNamePrefix = $state('');
	let actionLoading = $state(false);
	let selectedPool = $state('');

	let pools = resource(
		() => `pool-list-${hostname || 'local'}`,
		async (key) => {
			const results = await getPools(false, hostname);
			updateCache(key, results);
			return results;
		},
		{
			initialValue: []
		}
	);

	let poolOptions = $derived.by(() => {
		return pools.current.map((pool) => ({
			label: pool.name,
			value: pool.name
		}));
	});

	let comboBoxes = $state({
		pool: {
			open: false
		}
	});

	function normalizeTemplateName(label: string): string {
		return label.replace(/\s*\((?:CT\s*)?\d+\)\s*$/i, '').trim();
	}

	let templateName = $derived.by(() => {
		const cleaned = normalizeTemplateName(templateLabel);
		return cleaned || `Template ${templateId}`;
	});

	function resetForm() {
		createMode = 'single';
		singleCTID = nextGuestId || 0;
		singleName = '';
		multipleStartCTID = nextGuestId || 0;
		multipleCount = 1;
		multipleNamePrefix = templateName;
		selectedPool = pools.current[0]?.name || '';
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				pools.refetch();
				resetForm();
			}
		}
	);

	watch(
		() => pools.current,
		(loadedPools) => {
			if (open && !selectedPool && loadedPools.length > 0) {
				selectedPool = loadedPools[0].name;
			}
		}
	);

	function validateCreate(): string | null {
		if (!selectedPool) return 'Select a ZFS pool';

		if (createMode === 'single') {
			const ctid = Number(singleCTID);

			if (ctid < 1 || ctid > 9999) return 'Invalid CTID';
			if (singleName && !isValidVMName(singleName)) return 'Invalid Jail Name';
		}

		if (createMode === 'multiple') {
			const startCTID = Number(multipleStartCTID);
			const count = Number(multipleCount);
			const endCTID = startCTID + count - 1;

			if (count < 1) return 'Count must be positive';
			if (startCTID < 1 || endCTID > 9999) return 'Invalid CTID range';

			if (multipleNamePrefix) {
				if (multipleNamePrefix.length > 15 || !isValidVMName(multipleNamePrefix)) {
					return 'Invalid jail name prefix';
				}
			}
		}

		return null;
	}

	function templateCreateErrorMessage(error?: string): string {
		const err = (error || '').toLowerCase();

		if (err.includes('insufficient_pool_space')) return 'Not enough space in selected pool';
		if (err.includes('ctid_range_contains_used_values')) return 'One or more CTIDs are already in use';
		if (err.includes('duplicate_ctids_requested')) return 'Duplicate CTIDs in request';
		if (err.includes('invalid_ctid_range') || err.includes('invalid_ctid')) return 'Invalid CTID range';
		if (err.includes('jail_name_already_in_use')) return 'One or more jail names are already in use';
		if (err.includes('duplicate_jail_names_requested')) return 'Duplicate jail names in request';
		if (err.includes('invalid_name_prefix')) return 'Invalid jail name prefix';
		if (err.includes('invalid_jail_name')) return 'Invalid jail name';
		if (err.includes('mode') && err.includes('invalid')) return 'Invalid create mode';
		if (err.includes('pool_required')) return 'Select a ZFS pool';
		if (err.includes('pool_not_found')) return 'Selected pool is not available';
		if (err.includes('target_dataset_already_exists')) return 'Target jail dataset already exists';
		if (err.includes('template_dataset_not_found')) return 'Template dataset not found';

		return 'Failed to create jail from template';
	}

	async function create() {
		actionLoading = true;

		try {
			const validationError = validateCreate();
			if (validationError) {
				toast.error(validationError, { position: 'bottom-center' });
				return;
			}

			const result =
				createMode === 'single'
					? await createJailFromTemplate(
							templateId,
							{
								mode: 'single',
								ctid: Number(singleCTID),
								name: singleName || undefined,
								pool: selectedPool || undefined
							},
							hostname
						)
					: await createJailFromTemplate(
							templateId,
							{
								mode: 'multiple',
								startCtid: Number(multipleStartCTID),
								count: Number(multipleCount),
								namePrefix: multipleNamePrefix || undefined,
								pool: selectedPool || undefined
							},
							hostname
						);

			if (result.error) {
				toast.error(templateCreateErrorMessage(result.error), { position: 'bottom-center' });
				return;
			}

			open = false;
			reload.leftPanel = true;
			toast.success('Create jail request queued', { position: 'bottom-center' });
		} finally {
			actionLoading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-lg">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon icon-[hugeicons--prison]"></span>
					<span>Create Jail - Template {templateName}</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={() => resetForm()} title={'Reset'}>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => (open = false)}
						title={'Close'}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>
		<div class="grid gap-4 py-2">
			<CustomComboBox
				bind:open={comboBoxes.pool.open}
				label="Pool"
				bind:value={selectedPool}
				data={poolOptions}
				classes="flex-1 space-y-1"
				placeholder="Select ZFS pool"
				triggerWidth="w-full"
				width="w-full"
			/>

			<div class="flex gap-2">
				<Button
					size="sm"
					variant={createMode === 'single' ? 'default' : 'outline'}
					onclick={() => (createMode = 'single')}>Single</Button
				>
				<Button
					size="sm"
					variant={createMode === 'multiple' ? 'default' : 'outline'}
					onclick={() => (createMode = 'multiple')}>Multiple</Button
				>
			</div>

			{#if createMode === 'single'}
				<div class="grid gap-2">
					<CustomValueInput
						type="number"
						bind:value={singleCTID}
						label="CTID"
						placeholder="100"
						classes="w-full"
					/>
				</div>
				<div class="grid gap-2">
					<CustomValueInput
						type="text"
						bind:value={singleName}
						label="Name"
						placeholder="Name"
						classes="w-full"
					/>
				</div>
			{:else}
				<div class="grid grid-cols-2 gap-2">
					<CustomValueInput
						type="number"
						bind:value={multipleStartCTID}
						label="Starting CTID"
						placeholder="100"
						classes="w-full"
					/>

					<CustomValueInput
						type="number"
						bind:value={multipleCount}
						label="Count"
						placeholder="100"
						classes="w-full"
					/>
				</div>
				<div class="grid gap-2">
					<CustomValueInput
						type="text"
						bind:value={multipleNamePrefix}
						label="Name Prefix"
						placeholder="LB"
						classes="w-full"
					/>
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button size="sm" disabled={actionLoading} onclick={() => void create()}>
				{#if actionLoading}
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--loading] animate-spin h-4 w-4"></span>
						<span>Creating {createMode === 'single' ? 'Jail' : 'Jails'}</span>
					</div>
				{:else}
					<span>Create {createMode === 'single' ? 'Jail' : 'Jails'}</span>
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

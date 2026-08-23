<script lang="ts">
	import { createJailFromTemplate, getJailTemplateById } from '$lib/api/jail/jail';
	import { getPoolsResult } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { JailTemplate } from '$lib/types/jail/jail';
	import { handleAPIError, isAPIResponse } from '$lib/utils/http';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { isValidVMName } from '$lib/utils/string';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

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
	let loadingDependencies = $state(false);
	let template = $state<JailTemplate | null>(null);
	let availablePools = $state<{ name: string }[]>([]);
	let selectedPool = $state('');

	let poolOptions = $derived.by(() => {
		return availablePools.map((pool) => ({
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
		multipleNamePrefix = '';
		selectedPool = '';
	}

	async function loadDependencies() {
		loadingDependencies = true;
		template = null;
		availablePools = [];
		try {
			const [poolsResult, templateResult] = await Promise.all([
				getPoolsResult(false, { hostname }),
				getJailTemplateById(templateId, hostname)
			]);

			if (isAPIResponse(poolsResult)) {
				handleAPIError(poolsResult);
				throw new Error('Failed to load available pools');
			}
			if (isAPIResponse(templateResult)) {
				handleAPIError(templateResult);
				throw new Error('Failed to load template details');
			}

			availablePools = poolsResult;
			template = templateResult;
			const availablePoolNames = new Set(poolsResult.map((pool) => pool.name));
			selectedPool = availablePoolNames.has(templateResult.pool)
				? templateResult.pool
				: poolsResult[0]?.name || '';
		} catch (error) {
			availablePools = [];
			template = null;
			selectedPool = '';
			toast.error(error instanceof Error ? error.message : 'Failed to load template data', {
				position: 'bottom-center'
			});
		} finally {
			loadingDependencies = false;
		}
	}

	watch(
		() => open,
		(isOpen) => {
			if (isOpen) {
				resetForm();
				void loadDependencies();
			}
		}
	);

	function validateCreate(): string | null {
		if (!template) return 'Template details are not loaded yet';
		if (!selectedPool) return 'Select a ZFS pool';

		if (createMode === 'single') {
			const ctid = Number(singleCTID);

			if (!Number.isSafeInteger(ctid) || ctid < 1 || ctid > 9999) return 'Invalid CTID';
			if (singleName && !isValidVMName(singleName)) return 'Invalid Jail Name';
		}

		if (createMode === 'multiple') {
			const startCTID = Number(multipleStartCTID);
			const count = Number(multipleCount);
			const endCTID = startCTID + count - 1;

			if (!Number.isSafeInteger(count) || count < 1) return 'Count must be positive';
			if (count > 200) return 'Count cannot exceed 200';
			if (!Number.isSafeInteger(startCTID) || startCTID < 1 || endCTID > 9999)
				return 'Invalid CTID range';

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
		if (err.includes('ctid_range_contains_used_values'))
			return 'One or more CTIDs are already in use';
		if (err.includes('guest_id_already_in_use'))
			return 'One or more guest IDs are already used by a VM or jail';
		if (err.includes('guest_identity_inventory_conflict'))
			return 'Existing VM/jail ID conflicts must be resolved before creating a jail';
		if (err.includes('guest_identity_inventory_unavailable'))
			return 'Could not verify guest IDs on every cluster node. Check node health and retry';
		if (err.includes('guest_identity_inventory_scan_failed'))
			return 'Could not check VM and jail IDs. Check the server logs and retry';
		if (err.includes('duplicate_ctids_requested')) return 'Duplicate CTIDs in request';
		if (err.includes('invalid_ctid_range') || err.includes('invalid_ctid'))
			return 'Invalid CTID range';
		if (err.includes('jail_name_already_in_use'))
			return 'One or more jail names are already in use';
		if (err.includes('duplicate_jail_names_requested')) return 'Duplicate jail names in request';
		if (err.includes('invalid_name_prefix')) return 'Invalid jail name prefix';
		if (err.includes('invalid_jail_name')) return 'Invalid jail name';
		if (err.includes('mode') && err.includes('invalid')) return 'Invalid create mode';
		if (err.includes('pool_required')) return 'Select a ZFS pool';
		if (err.includes('pool_not_found')) return 'Selected pool is not available';
		if (err.includes('target_dataset_already_exists')) return 'Target jail dataset already exists';
		if (err.includes('template_dataset_not_found')) return 'Template dataset not found';
		if (err.includes('template_network_switch_not_found'))
			return 'One or more template switches do not exist';

		return 'Failed to create jail from template';
	}

	async function create() {
		if (actionLoading || loadingDependencies) return;

		const validationError = validateCreate();
		if (validationError) {
			toast.error(validationError, { position: 'bottom-center' });
			return;
		}

		actionLoading = true;
		try {
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

			if (isAPIResponse(result)) {
				handleAPIError(result);
				const error = Array.isArray(result.error) ? result.error[0] : result.error;
				toast.error(templateCreateErrorMessage(error), { position: 'bottom-center' });
				return;
			}

			open = false;
			reload.leftPanel = true;
			toast.success('Create jail request queued', { position: 'bottom-center' });
		} catch (error) {
			toast.error(error instanceof Error ? error.message : 'Failed to create jail from template', {
				position: 'bottom-center'
			});
		} finally {
			actionLoading = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="max-w-lg"
		showResetButton={true}
		onReset={() => resetForm()}
		onClose={() => (open = false)}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="text-left">
				<SpanWithIcon
					icon="icon-[hugeicons--prison]"
					size="h-5 w-5"
					gap="gap-2"
					title="Create Jail - Template {templateName}"
				/>
			</Dialog.Title>
		</Dialog.Header>
		{#if loadingDependencies}
			<div class="flex items-center justify-center gap-2 py-4 text-sm text-muted-foreground">
				<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
				Loading template details...
			</div>
		{:else if !template}
			<div class="py-4 text-center text-sm text-muted-foreground">Template data unavailable</div>
		{/if}
		<div class="grid gap-4 py-0">
			<CustomComboBox
				bind:open={comboBoxes.pool.open}
				label="Pool"
				bind:value={selectedPool}
				data={poolOptions}
				classes="flex-1 space-y-1"
				placeholder="Select ZFS pool"
				disabled={loadingDependencies || !template || actionLoading}
				triggerWidth="w-full"
				width="w-full"
			/>

			<div class="flex gap-2">
				<Button
					size="sm"
					variant={createMode === 'single' ? 'default' : 'outline'}
					disabled={loadingDependencies || !template || actionLoading}
					onclick={() => (createMode = 'single')}>Single</Button
				>
				<Button
					size="sm"
					variant={createMode === 'multiple' ? 'default' : 'outline'}
					disabled={loadingDependencies || !template || actionLoading}
					onclick={() => (createMode = 'multiple')}>Multiple</Button
				>
			</div>

			{#if createMode === 'single'}
				<div class="grid gap-2">
					<CustomValueInput
						type="number"
						bind:value={singleCTID}
						label="CTID"
						disabled={loadingDependencies || !template || actionLoading}
						placeholder="100"
						classes="w-full space-y-1"
					/>
				</div>
				<div class="grid gap-2">
					<CustomValueInput
						type="text"
						bind:value={singleName}
						label="Name"
						disabled={loadingDependencies || !template || actionLoading}
						placeholder="Name"
						classes="w-full space-y-1"
					/>
				</div>
			{:else}
				<div class="grid grid-cols-2 gap-2">
					<CustomValueInput
						type="number"
						bind:value={multipleStartCTID}
						label="Starting CTID"
						disabled={loadingDependencies || !template || actionLoading}
						placeholder="100"
						classes="w-full space-y-1"
					/>

					<CustomValueInput
						type="number"
						bind:value={multipleCount}
						label="Count"
						disabled={loadingDependencies || !template || actionLoading}
						placeholder="100"
						classes="w-full space-y-1"
					/>
				</div>
				<div class="grid gap-2">
					<CustomValueInput
						type="text"
						bind:value={multipleNamePrefix}
						label="Name Prefix"
						disabled={loadingDependencies || !template || actionLoading}
						placeholder="LB"
						classes="w-full space-y-1"
					/>
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<Button
				size="sm"
				disabled={actionLoading || loadingDependencies || !template || !selectedPool}
				onclick={() => void create()}
			>
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

<script lang="ts">
	import { createVMFromTemplate, getVMTemplateById, type CreateVMFromTemplateRequest } from '$lib/api/vm/vm';
	import { getPools } from '$lib/api/zfs/pool';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { reload } from '$lib/stores/api.svelte';
	import type { VMTemplate } from '$lib/types/vm/vm';
	import { isValidVMName } from '$lib/utils/string';
	import { watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		templateId: number;
		templateLabel: string;
		hostname?: string;
		nextGuestId: number;
	}

	let { open = $bindable(), templateId, templateLabel, hostname, nextGuestId }: Props = $props();

	let template = $state<VMTemplate | null>(null);
	let loadingTemplate = $state(false);
	let createMode = $state<'single' | 'multiple'>('single');
	let actionLoading = $state(false);
	let availablePools = $state<{ name: string }[]>([]);
	let storagePoolBySourceId = $state<Record<number, string>>({});

	let singleRID = $state(nextGuestId || 0);
	let singleName = $state('');
	let multipleStartRID = $state(nextGuestId || 0);
	let multipleCount = $state(1);
	let multipleNamePrefix = $state('');

	let rewriteCloudInitIdentity = $state(false);
	let cloudInitPrefix = $state('');

	function hasCloudInit(vmTemplate: VMTemplate | null): boolean {
		if (!vmTemplate) {
			return false;
		}
		return Boolean(
			vmTemplate.cloudInitData?.trim() ||
				vmTemplate.cloudInitMetaData?.trim() ||
				vmTemplate.cloudInitNetworkConfig?.trim()
		);
	}

	let hasTemplateCloudInit = $derived.by(() => hasCloudInit(template));

	function normalizedTemplateName(label: string): string {
		return label.replace(/\s*\((?:VM\s*)?\d+\)\s*$/i, '').trim();
	}

	function resetForm() {
		createMode = 'single';
		singleRID = nextGuestId || 0;
		singleName = '';
		multipleStartRID = nextGuestId || 0;
		multipleCount = 1;
		multipleNamePrefix = normalizedTemplateName(templateLabel) || 'vm';
		rewriteCloudInitIdentity = false;
		cloudInitPrefix = '';
	}

	async function loadDependencies() {
		loadingTemplate = true;
		try {
			const [pools, vmTemplate] = await Promise.all([
				getPools(false, hostname),
				getVMTemplateById(templateId, hostname)
			]);

			availablePools = pools || [];
			template = vmTemplate;

			const mapping: Record<number, string> = {};
			for (const storage of vmTemplate.storages || []) {
				mapping[storage.sourceStorageId] = storage.pool || pools?.[0]?.name || '';
			}
			storagePoolBySourceId = mapping;
		} catch {
			template = null;
			availablePools = [];
			storagePoolBySourceId = {};
			toast.error('Failed to load template data', { position: 'bottom-center' });
		} finally {
			loadingTemplate = false;
		}
	}

	function updateStoragePool(sourceStorageId: number, pool: string) {
		storagePoolBySourceId = {
			...storagePoolBySourceId,
			[sourceStorageId]: pool
		};
	}

	function validateRequest(): string | null {
		if (!template) {
			return 'Template details are not loaded yet';
		}

		if ((template.storages || []).length === 0) {
			return 'Template has no cloneable storage';
		}

		for (const storage of template.storages || []) {
			const pool = (storagePoolBySourceId[storage.sourceStorageId] || '').trim();
			if (!pool) {
				return `Select a pool for storage ${storage.sourceStorageId}`;
			}
		}

		if (createMode === 'single') {
			const rid = Number(singleRID);
			if (rid < 1 || rid > 9999) {
				return 'Invalid RID';
			}
			if (singleName && !isValidVMName(singleName)) {
				return 'Invalid VM name';
			}
		} else {
			const startRID = Number(multipleStartRID);
			const count = Number(multipleCount);
			const endRID = startRID + count - 1;

			if (count < 1) {
				return 'Count must be positive';
			}
			if (startRID < 1 || endRID > 9999) {
				return 'Invalid RID range';
			}
			if (multipleNamePrefix && !isValidVMName(multipleNamePrefix)) {
				return 'Invalid VM name prefix';
			}
		}

		return null;
	}

	function toStorageMappings() {
		if (!template) {
			return [];
		}

		return (template.storages || []).map((storage) => ({
			sourceStorageId: storage.sourceStorageId,
			pool: storagePoolBySourceId[storage.sourceStorageId] || storage.pool || ''
		}));
	}

	function createErrorMessage(error?: string): string {
		const err = (error || '').toLowerCase();
		if (err.includes('template_network_switch_not_found')) return 'One or more template switches do not exist';
		if (err.includes('template_storage_dataset_not_found')) return 'Template storage dataset was not found';
		if (err.includes('template_has_no_cloneable_storage')) return 'Template has no cloneable storage';
		if (err.includes('rid_range_contains_used_values')) return 'One or more RIDs are already in use';
		if (err.includes('vm_name_already_in_use')) return 'One or more VM names are already in use';
		if (err.includes('invalid_rid') || err.includes('invalid_rid_range')) return 'Invalid RID or RID range';
		if (err.includes('invalid_vm_name')) return 'Invalid VM name';
		if (err.includes('invalid_name_prefix')) return 'Invalid VM name prefix';
		if (err.includes('insufficient_pool_space')) return 'Not enough free space in selected pool(s)';
		if (err.includes('pool_not_found')) return 'Selected pool is not available';
		if (err.includes('storage_pool_required')) return 'Select pools for all storages';
		if (err.includes('invalid_cloud_init_metadata_yaml')) return 'Cloud-init metadata YAML is invalid in template';
		return 'Failed to create VM from template';
	}

	async function create() {
		const validationError = validateRequest();
		if (validationError) {
			toast.error(validationError, { position: 'bottom-center' });
			return;
		}

		actionLoading = true;
		try {
			const payload: CreateVMFromTemplateRequest =
				createMode === 'single'
					? {
							mode: 'single',
							rid: Number(singleRID),
							name: singleName || undefined,
							storagePools: toStorageMappings(),
							rewriteCloudInitIdentity: hasTemplateCloudInit ? rewriteCloudInitIdentity : false,
							cloudInitPrefix:
								hasTemplateCloudInit && rewriteCloudInitIdentity
									? (cloudInitPrefix || undefined)
									: undefined
						}
					: {
							mode: 'multiple',
							startRid: Number(multipleStartRID),
							count: Number(multipleCount),
							namePrefix: multipleNamePrefix || undefined,
							storagePools: toStorageMappings(),
							rewriteCloudInitIdentity: hasTemplateCloudInit ? rewriteCloudInitIdentity : false,
							cloudInitPrefix:
								hasTemplateCloudInit && rewriteCloudInitIdentity
									? (cloudInitPrefix || undefined)
									: undefined
						};

			const result = await createVMFromTemplate(templateId, payload, hostname);
			if (result.error) {
				toast.error(createErrorMessage(result.error), { position: 'bottom-center' });
				return;
			}

			open = false;
			reload.leftPanel = true;
			toast.success('Create VM request queued', { position: 'bottom-center' });
		} finally {
			actionLoading = false;
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
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-3xl">
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--monitor-outline]"></span>
					<span>Create VM - Template {normalizedTemplateName(templateLabel)}</span>
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

		{#if loadingTemplate}
			<div class="py-8 text-center text-sm text-muted-foreground">
				<span class="icon-[mdi--loading] mr-2 inline-block h-4 w-4 animate-spin"></span>
				Loading template details...
			</div>
		{:else if !template}
			<div class="py-8 text-center text-sm text-muted-foreground">Template data unavailable</div>
		{:else}
			<div class="grid gap-4 py-2">
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
					<div class="grid grid-cols-2 gap-2">
						<CustomValueInput
							type="number"
							bind:value={singleRID}
							label="RID"
							placeholder="100"
							classes="w-full"
						/>
						<CustomValueInput
							type="text"
							bind:value={singleName}
							label="Name"
							placeholder="Optional"
							classes="w-full"
						/>
					</div>
				{:else}
					<div class="grid grid-cols-3 gap-2">
						<CustomValueInput
							type="number"
							bind:value={multipleStartRID}
							label="Starting RID"
							placeholder="100"
							classes="w-full"
						/>
						<CustomValueInput
							type="number"
							bind:value={multipleCount}
							label="Count"
							placeholder="1"
							classes="w-full"
						/>
						<CustomValueInput
							type="text"
							bind:value={multipleNamePrefix}
							label="Name Prefix"
							placeholder="vm"
							classes="w-full"
						/>
					</div>
				{/if}

				<div class="border rounded-md p-3 space-y-2">
					<div class="text-sm font-medium">Storage Pool Mapping</div>
					{#each template.storages as storage}
						<div class="grid grid-cols-[1fr_180px] gap-2 items-center">
							<div class="text-xs text-muted-foreground">
								Storage #{storage.sourceStorageId} ({storage.type.toUpperCase()})
							</div>
							<select
								class="select select-sm border"
								value={storagePoolBySourceId[storage.sourceStorageId] || ''}
								onchange={(e) =>
									updateStoragePool(
										storage.sourceStorageId,
										(e.currentTarget as HTMLSelectElement).value
									)}
							>
								<option value="" disabled selected={!storagePoolBySourceId[storage.sourceStorageId]}>
									Select pool
								</option>
								{#each availablePools as pool}
									<option value={pool.name}>{pool.name}</option>
								{/each}
							</select>
						</div>
					{/each}
				</div>

				{#if hasTemplateCloudInit}
					<div class="border rounded-md p-3 space-y-2">
						<label class="flex items-center gap-2 text-sm">
							<input type="checkbox" bind:checked={rewriteCloudInitIdentity} />
							Rewrite cloud-init hostname + instance-id
						</label>

						{#if rewriteCloudInitIdentity}
							<CustomValueInput
								type="text"
								bind:value={cloudInitPrefix}
								label="Identity Prefix (optional)"
								placeholder="If empty, VM name is used"
								classes="w-full"
							/>
						{/if}
					</div>
				{/if}
			</div>
		{/if}

		<Dialog.Footer>
			<Button size="sm" disabled={actionLoading || loadingTemplate || !template} onclick={() => void create()}>
				{#if actionLoading}
					<div class="flex items-center gap-2">
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						<span>Creating {createMode === 'single' ? 'VM' : 'VMs'}</span>
					</div>
				{:else}
					<span>Create {createMode === 'single' ? 'VM' : 'VMs'}</span>
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

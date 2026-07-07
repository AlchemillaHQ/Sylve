<script lang="ts">
	import {
		addLUN,
		addPortal,
		createTarget,
		deleteTarget,
		getTargetSessions,
		getTargets,
		removeLUN,
		removePortal,
		updateTarget
	} from '$lib/api/iscsi/target';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import Combobox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { storage } from '$lib';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { TargetSessions } from '$lib/api/iscsi/target';
	import type { ISCSITarget } from '$lib/types/iscsi/target';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateComboboxOptions } from '$lib/utils/input';
	import { generateCHAPSecret } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, useInterval, watch, IsDocumentVisible } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		targets: ISCSITarget[];
		volumes: Dataset[];
		sessions: TargetSessions;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let targets = resource(
		() => 'iscsi-targets',
		async () => {
			const result = await getTargets();
			updateCache('iscsi-targets', result);
			return result;
		},
		{ initialValue: data.targets }
	);

	// svelte-ignore state_referenced_locally
	let volumes = resource(
		() => 'zfs-volumes',
		async () => {
			const result = await getDatasets(GZFSDatasetTypeSchema.enum.VOLUME);
			updateCache('zfs-volumes', result);
			return result;
		},
		{ initialValue: data.volumes }
	);

	// svelte-ignore state_referenced_locally
	let sessions = resource(
		() => 'iscsi-target-sessions',
		async () => {
			const result = await getTargetSessions();
			updateCache('iscsi-target-sessions', result);
			return result;
		},
		{ initialValue: data.sessions }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				targets.refetch();
				sessions.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let activeTarget: ISCSITarget | null = $derived(
		activeRow?.id ? (targets.current.find((t) => t.id === Number(activeRow?.id)) ?? null) : null
	);

	const blankForm = () => ({
		targetName: '',
		alias: '',
		authMethod: 'None',
		chapName: '',
		chapSecret: '',
		mutualChapName: '',
		mutualChapSecret: ''
	});

	const blankPortalForm = () => ({ address: '', port: 3260 });
	const blankLUNForm = () => ({ lunNumber: 0, zvol: '' });

	let form = $state(blankForm());
	let portalForm = $state(blankPortalForm());
	let lunForm = $state(blankLUNForm());

	let properties = $state({
		create: { open: false },
		edit: { open: false },
		delete: { open: false }
	});

	let loading = $state(false);
	let query = $state('');
	let zvolComboOpen = $state(false);
	let visible = new IsDocumentVisible();

	let usedZvols = $derived(new Set(editTarget?.luns.map((l) => l.zvol) ?? []));

	let availableVolumes = $derived(
		volumes.current.filter((v) => !v.name.includes('/sylve/virtual-machines/') && !usedZvols.has(v.name))
	);

	function openCreate() {
		form = blankForm();
		properties.create.open = true;
	}

	function openEdit() {
		if (!activeTarget) return;
		form = {
			targetName: activeTarget.targetName,
			alias: activeTarget.alias,
			authMethod: activeTarget.authMethod,
			chapName: activeTarget.chapName,
			chapSecret: activeTarget.chapSecret,
			mutualChapName: activeTarget.mutualChapName,
			mutualChapSecret: activeTarget.mutualChapSecret
		};
		portalForm = blankPortalForm();
		lunForm = blankLUNForm();
		properties.edit.open = true;
	}

	function validateChapSecrets(): boolean {
		if (form.authMethod === 'CHAP' || form.authMethod === 'MutualCHAP') {
			if (form.chapSecret.length < 12 || form.chapSecret.length > 16) {
				toast.error('CHAP Secret must be 12-16 characters (RFC 3720)', {
					position: 'bottom-center'
				});
				return false;
			}
		}
		if (form.authMethod === 'MutualCHAP') {
			if (form.mutualChapSecret.length < 12 || form.mutualChapSecret.length > 16) {
				toast.error('Mutual CHAP Secret must be 12-16 characters (RFC 3720)', {
					position: 'bottom-center'
				});
				return false;
			}
		}
		return true;
	}

	async function submitCreate() {
		if (!validateChapSecrets()) return;
		loading = true;
		const response = await createTarget(
			form.targetName,
			form.alias,
			form.authMethod,
			form.chapName,
			form.chapSecret,
			form.mutualChapName,
			form.mutualChapSecret
		);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to create target', { position: 'bottom-center' });
			return;
		}
		toast.success('Target created', { position: 'bottom-center' });
		properties.create.open = false;
		reload = true;
	}

	async function submitEdit() {
		if (!activeTarget) return;
		if (!validateChapSecrets()) return;
		loading = true;
		const response = await updateTarget(
			activeTarget.id,
			form.targetName,
			form.alias,
			form.authMethod,
			form.chapName,
			form.chapSecret,
			form.mutualChapName,
			form.mutualChapSecret
		);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update target', { position: 'bottom-center' });
			return;
		}
		toast.success('Target updated', { position: 'bottom-center' });
		properties.edit.open = false;
		reload = true;
	}

	async function submitAddPortal() {
		if (!activeTarget) return;

		if (!portalForm.address.trim()) {
			toast.error('Portal address is required', { position: 'bottom-center' });
			return;
		}
		const port = Number(portalForm.port);
		if (!portalForm.port || isNaN(port) || port < 1 || port > 65535) {
			toast.error('Port must be a number between 1 and 65535', { position: 'bottom-center' });
			return;
		}
		if (editTarget?.portals.some((p) => p.address === portalForm.address && p.port === port)) {
			toast.error('A portal with this address and port already exists', {
				position: 'bottom-center'
			});
			return;
		}

		loading = true;
		const response = await addPortal(activeTarget.id, portalForm.address, port);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to add portal', { position: 'bottom-center' });
			return;
		}
		toast.success('Portal added', { position: 'bottom-center' });
		portalForm = { address: '', port: port + 1 };
		reload = true;
	}

	async function submitRemovePortal(portalId: number) {
		loading = true;
		const response = await removePortal(portalId);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to remove portal', { position: 'bottom-center' });
			return;
		}
		toast.success('Portal removed', { position: 'bottom-center' });
		reload = true;
	}

	async function submitAddLUN() {
		if (!activeTarget) return;

		const lun = Number(lunForm.lunNumber);
		if (isNaN(lun) || lun < 0) {
			toast.error('LUN number must be a non-negative integer', { position: 'bottom-center' });
			return;
		}
		if (!lunForm.zvol) {
			toast.error('Please select a ZFS Volume', { position: 'bottom-center' });
			return;
		}
		if (editTarget?.luns.some((l) => l.lunNumber === lun)) {
			toast.error('A LUN with this number already exists', { position: 'bottom-center' });
			return;
		}

		loading = true;
		const response = await addLUN(activeTarget.id, lun, lunForm.zvol);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to add LUN', { position: 'bottom-center' });
			return;
		}
		toast.success('LUN added', { position: 'bottom-center' });
		lunForm = blankLUNForm();
		reload = true;
	}

	async function submitRemoveLUN(lunId: number) {
		loading = true;
		const response = await removeLUN(lunId);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to remove LUN', { position: 'bottom-center' });
			return;
		}
		toast.success('LUN removed', { position: 'bottom-center' });
		reload = true;
	}

	function generateTableData(
		targets: ISCSITarget[],
		sessions: TargetSessions
	): { rows: Row[]; columns: Column[] } {
		const columns: Column[] = [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'connectionStatus',
				title: 'Status',
				width: '5%',
				formatter: (cell) => {
					const val: string = cell.getValue() ?? 'Unknown';
					const connected = val.startsWith('Connected');
					return renderWithIcon(
						connected ? 'mdi:check-circle' : 'mdi:close-circle',
						val,
						connected ? 'text-green-500' : 'text-muted-foreground'
					);
				}
			},
			{ field: 'targetName', title: 'Target Name (IQN)' },
			{ field: 'alias', title: 'Alias', formatter: (cell) => cell.getValue() || '-' },
			{ field: 'authMethod', title: 'Auth Method' },
			{ field: 'portalCount', title: 'Portals', width: '8%' },
			{ field: 'lunCount', title: 'LUNs', width: '8%' },
			{
				field: 'createdAt',
				title: 'Created At',
				formatter: (cell) => convertDbTime(cell.getValue())
			}
		];

		const rows: Row[] = targets.map((tgt) => ({
			id: tgt.id,
			connectionStatus: sessions[tgt.targetName] ? `Connected (${sessions[tgt.targetName]})` : 'Idle',
			targetName: tgt.targetName,
			alias: tgt.alias || '-',
			authMethod: tgt.authMethod,
			portalCount: tgt.portals?.length ?? 0,
			lunCount: tgt.luns?.length ?? 0,
			createdAt: tgt.createdAt
		}));

		return { rows, columns };
	}

	let tableData = $derived(generateTableData(targets.current, sessions.current));

	// Updated active target after refetch
	let editTarget: ISCSITarget | null = $derived(
		activeTarget ? (targets.current.find((t) => t.id === activeTarget!.id) ?? activeTarget) : null
	);

	useInterval(5000, {
		callback: () => {
			if (visible.current && !storage.idle) {
				sessions.refetch();
			}
		}
	});
</script>

{#snippet targetForm(title: string, onSubmit: () => void, submitLabel: string, onClose: () => void)}
	<Dialog.Header>
		<Dialog.Title>
			<SpanWithIcon icon="icon-[mdi--server]" size="h-5 w-5" gap="gap-2" {title} />
		</Dialog.Title>
	</Dialog.Header>
	<form
		onsubmit={(e) => {
			e.preventDefault();
			onSubmit();
		}}
	>
		<input type="text" style="display:none" autocomplete="username" />
		<input type="password" style="display:none" autocomplete="new-password" />

		<div class="max-h-[62vh] overflow-y-auto pr-1">
			<div class="grid grid-cols-2 gap-x-4 gap-y-3 py-1">
				<div>
					<CustomValueInput
						label="Target Name (IQN)"
						placeholder="iqn.2025-01.com.example:target0"
						bind:value={form.targetName}
						classes="grid gap-1.5"
					/>
				</div>
				<div>
					<CustomValueInput
						label="Alias"
						placeholder="My Storage Target"
						bind:value={form.alias}
						classes="grid gap-1.5"
					/>
				</div>
				<div class="col-span-2 grid gap-1.5">
					<Label>Auth Method</Label>
					<Select.Root type="single" bind:value={form.authMethod}>
						<Select.Trigger class="w-full">
							{form.authMethod}
						</Select.Trigger>
						<Select.Content>
							<Select.Item value="None">None</Select.Item>
							<Select.Item value="CHAP">CHAP (one-way)</Select.Item>
							<Select.Item value="MutualCHAP">MutualCHAP (two-way)</Select.Item>
						</Select.Content>
					</Select.Root>
				</div>

				{#if form.authMethod === 'CHAP' || form.authMethod === 'MutualCHAP'}
					<div class="col-span-2">
						<div class="grid grid-cols-2 gap-x-4 gap-y-3">
							<CustomValueInput
								label="CHAP Name"
								placeholder="user1"
								bind:value={form.chapName}
								classes="grid gap-1.5"
							/>
							<CustomValueInput
								label="CHAP Secret"
								placeholder="Password (12-16 characters)"
								type="password"
								bind:value={form.chapSecret}
								classes="grid gap-1.5"
								revealOnFocus={true}
								topRightButton={{
									icon: 'icon-[fad--random-2dice]',
									tooltip: 'Generate Secret',
									function: async () => generateCHAPSecret()
								}}
							/>
						</div>
					</div>
				{/if}

				{#if form.authMethod === 'MutualCHAP'}
					<div class="col-span-2">
						<div class="grid grid-cols-2 gap-x-4 gap-y-3">
							<CustomValueInput
								label="Mutual CHAP Name"
								placeholder="mutualuser1"
								bind:value={form.mutualChapName}
								classes="grid gap-1.5"
							/>
							<CustomValueInput
								label="Mutual CHAP Secret"
								placeholder="Password (12-16 characters)"
								type="password"
								bind:value={form.mutualChapSecret}
								classes="grid gap-1.5"
								revealOnFocus={true}
								topRightButton={{
									icon: 'icon-[fad--random-2dice]',
									tooltip: 'Generate Secret',
									function: async () => generateCHAPSecret()
								}}
							/>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<Dialog.Footer class="mt-4">
			<Button type="submit" disabled={loading}>{submitLabel}</Button>
		</Dialog.Footer>
	</form>
{/snippet}

{#snippet editTargetDialog()}
	<Dialog.Header>
		<Dialog.Title>
			<SpanWithIcon
				icon="icon-[mdi--server]"
				size="h-5 w-5"
				gap="gap-2"
				title="Edit iSCSI Target"
			/>
		</Dialog.Title>
	</Dialog.Header>

	<Tabs.Root value="details" class="flex flex-col min-h-0 flex-1">
		<Tabs.List class="grid w-full grid-cols-3 p-0">
			<Tabs.Trigger class="border-b" value="details">Details</Tabs.Trigger>
			<Tabs.Trigger class="border-b" value="portals">Portals</Tabs.Trigger>
			<Tabs.Trigger class="border-b" value="luns">LUNs</Tabs.Trigger>
		</Tabs.List>

		<Tabs.Content value="details" class="flex flex-col min-h-0 flex-1">
			<form
				class="flex flex-col min-h-0 flex-1"
				onsubmit={(e) => {
					e.preventDefault();
					submitEdit();
				}}
			>
				<div class="flex-1 overflow-y-auto">
					<div class="grid grid-cols-2 gap-x-4 gap-y-3">
						<div>
							<CustomValueInput
								label="Target Name (IQN)"
								placeholder="iqn.2025-01.com.example:target0"
								bind:value={form.targetName}
								classes="grid gap-1.5"
							/>
						</div>
						<div>
							<CustomValueInput
								label="Alias"
								placeholder="My Storage Target"
								bind:value={form.alias}
								classes="grid gap-1.5"
							/>
						</div>
						<div class="col-span-2 grid gap-1.5">
							<Label>Auth Method</Label>
							<Select.Root type="single" bind:value={form.authMethod}>
								<Select.Trigger class="w-full">
									{form.authMethod}
								</Select.Trigger>
								<Select.Content>
									<Select.Item value="None">None</Select.Item>
									<Select.Item value="CHAP">CHAP (one-way)</Select.Item>
									<Select.Item value="MutualCHAP">MutualCHAP (two-way)</Select.Item>
								</Select.Content>
							</Select.Root>
						</div>
						{#if form.authMethod === 'CHAP' || form.authMethod === 'MutualCHAP'}
							<div class="col-span-2">
								<div class="grid grid-cols-2 gap-x-4 gap-y-3">
									<CustomValueInput
										label="CHAP Name"
										placeholder="user1"
										bind:value={form.chapName}
										classes="grid gap-1.5"
									/>
									<CustomValueInput
										label="CHAP Secret"
										placeholder="Password (12-16 characters)"
										type="password"
										bind:value={form.chapSecret}
										classes="grid gap-1.5"
										revealOnFocus={true}
									/>
								</div>
							</div>
						{/if}
						{#if form.authMethod === 'MutualCHAP'}
							<div class="col-span-2">
								<div class="grid grid-cols-2 gap-x-4 gap-y-3">
									<CustomValueInput
										label="Mutual CHAP Name"
										placeholder="mutualuser1"
										bind:value={form.mutualChapName}
										classes="grid gap-1.5"
									/>
									<CustomValueInput
										label="Mutual CHAP Secret"
										placeholder="Password (12-16 characters)"
										type="password"
										bind:value={form.mutualChapSecret}
										classes="grid gap-1.5"
										revealOnFocus={true}
									/>
								</div>
							</div>
						{/if}
					</div>
				</div>
				<div class="flex justify-end pt-3">
					<Button type="submit" size="sm" disabled={loading}>Save Details</Button>
				</div>
			</form>
		</Tabs.Content>

		<Tabs.Content value="portals" class="flex flex-col min-h-0 flex-1">
			{#if editTarget && editTarget.portals.length > 0}
				<div class="flex-1 overflow-y-auto">
					<div class="flex flex-col gap-3">
						{#each editTarget.portals as portal (portal.id)}
							<div class="flex items-center justify-between rounded-md border p-3">
								<div class="flex flex-col gap-0.5">
									<span
									class="text-sm font-medium font-mono cursor-pointer"
									onclick={async () => {
										await navigator.clipboard.writeText(`${portal.address}:${portal.port}`);
										toast.success('Copied portal address to clipboard', {
											duration: 2000,
											position: 'bottom-center'
										});
									}}
								>{portal.address}:{portal.port}</span>
								</div>
								<Button
									size="sm"
									variant="outline"
									disabled={loading}
									onclick={() => submitRemovePortal(portal.id)}
								>
									<span class="icon-[mdi--trash-can-outline] h-4 w-4 text-destructive"></span>
								</Button>
							</div>
						{/each}
					</div>
				</div>
			{:else}
				<div class="flex flex-1 items-center justify-center">
					<div class="text-sm text-muted-foreground">No portals configured</div>
				</div>
			{/if}
			<form
				class="flex items-end gap-2 pt-3"
				onsubmit={(e) => {
					e.preventDefault();
					submitAddPortal();
				}}
			>
				<div class="flex-1">
					<CustomValueInput
						label="Address"
						placeholder="192.168.1.10"
						bind:value={portalForm.address}
						classes="grid gap-1.5"
					/>
				</div>
				<div class="w-28">
					<CustomValueInput
						label="Port"
						placeholder="3260"
						bind:value={portalForm.port}
						classes="grid gap-1.5"
					/>
				</div>
				<Button type="submit" size="sm" class="mb-0.5 h-9" disabled={loading}>Add</Button>
			</form>
		</Tabs.Content>

		<Tabs.Content value="luns" class="flex flex-col min-h-0 flex-1">
			{#if editTarget && editTarget.luns.length > 0}
				<div class="flex-1 overflow-y-auto">
					<div class="flex flex-col gap-3">
						{#each editTarget.luns as lun (lun.id)}
							<div class="flex items-center justify-between rounded-md border p-3">
								<div class="flex flex-col gap-0.5">
									<span class="text-sm font-medium">LUN {lun.lunNumber}</span>
									<span class="font-mono text-xs text-muted-foreground">/dev/zvol/{lun.zvol}</span>
								</div>
								<Button
									size="sm"
									variant="outline"
									disabled={loading}
									onclick={() => submitRemoveLUN(lun.id)}
								>
									<span class="icon-[mdi--trash-can-outline] h-4 w-4 text-destructive"></span>
								</Button>
							</div>
						{/each}
					</div>
				</div>
			{:else}
				<div class="flex flex-1 items-center justify-center">
					<div class="text-sm text-muted-foreground">No LUNs configured</div>
				</div>
			{/if}
			<form
				class="flex items-end gap-2 pt-3"
				onsubmit={(e) => {
					e.preventDefault();
					submitAddLUN();
				}}
			>
				<div class="w-24">
					<CustomValueInput
						label="LUN #"
						placeholder="0"
						bind:value={lunForm.lunNumber}
						classes="grid gap-1.5"
					/>
				</div>
				<div class="flex-1 min-w-0">
					<Combobox
						label="ZFS Volume"
						bind:open={zvolComboOpen}
						bind:value={lunForm.zvol}
						placeholder="Select a volume..."
						triggerWidth="w-full"
						width="w-full"
						disabled={availableVolumes.length === 0}
						data={generateComboboxOptions(availableVolumes.map((v) => v.name))}
					/>
				</div>
				<Button type="submit" size="sm" class="mb-0.5 h-9" disabled={loading || availableVolumes.length === 0}>Add</Button>
			</form>
		</Tabs.Content>
	</Tabs.Root>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreate} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{#if activeRows !== null && activeRows.length === 1}
			<Button onclick={openEdit} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit Target" />
			</Button>

			<Button
				onclick={() => (properties.delete.open = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete Target" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="iscsi-targets-tt"
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
		bind:query
	/>
</div>

<Dialog.Root bind:open={properties.create.open}>
	<Dialog.Content
		class="sm:max-w-145"
		showCloseButton={true}
		onClose={() => (properties.create.open = false)}
	>
		{@render targetForm(
			'New iSCSI Target',
			submitCreate,
			'Create',
			() => (properties.create.open = false)
		)}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={properties.edit.open}>
	<Dialog.Content
		class="sm:max-w-160 h-[85vh] max-h-[90vh] lg:h-[clamp(400px,45vh,480px)] lg:max-h-none flex flex-col"
		showCloseButton={true}
		onClose={() => (properties.edit.open = false)}
	>
		{@render editTargetDialog()}
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={properties.delete.open}
	names={{ parent: 'iSCSI target', element: activeRow ? String(activeRow.targetName) : '' }}
	actions={{
		onConfirm: async () => {
			if (activeTarget) {
				const response = await deleteTarget(activeTarget.id);
				if (response.status === 'error') {
					handleAPIError(response);
					toast.error('Failed to delete target', { position: 'bottom-center' });
					return;
				}
				toast.success('Target deleted', { position: 'bottom-center' });
				properties.delete.open = false;
				activeRows = null;
				reload = true;
			}
		},
		onCancel: () => {
			properties.delete.open = false;
		}
	}}
/>

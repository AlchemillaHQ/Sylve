<script lang="ts">
	import {
		createInitiator,
		deleteInitiator,
		getInitiators,
		getISCSIStatus,
		updateInitiator
	} from '$lib/api/iscsi/initiator';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { ISCSIInitiator, ISCSIStatus } from '$lib/types/iscsi/initiator';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { convertDbTime } from '$lib/utils/time';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		initiators: ISCSIInitiator[];
		status: ISCSIStatus;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let initiators = resource(
		() => 'iscsi-initiators',
		async () => {
			const result = await getInitiators();
			updateCache('iscsi-initiators', result);
			return result;
		},
		{ initialValue: data.initiators }
	);

	// svelte-ignore state_referenced_locally
	let status = resource(
		() => 'iscsi-status',
		async () => {
			const result = await getISCSIStatus();
			updateCache('iscsi-status', result);
			return result;
		},
		{ initialValue: data.status }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				initiators.refetch();
				status.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	const blankForm = () => ({
		nickname: '',
		targetAddress: '',
		targetName: '',
		initiatorName: '',
		authMethod: 'None',
		chapName: '',
		chapSecret: '',
		tgtChapName: '',
		tgtChapSecret: ''
	});

	let form = $state(blankForm());

	let properties = $state({
		create: { open: false },
		edit: { open: false },
		delete: { open: false }
	});

	let loading = $state(false);
	let query = $state('');

	function openCreate() {
		form = blankForm();
		properties.create.open = true;
	}

	function openEdit() {
		const initiator = initiators.current.find((i) => i.id === Number(activeRow?.id));
		if (!initiator) return;
		form = {
			nickname: initiator.nickname,
			targetAddress: initiator.targetAddress,
			targetName: initiator.targetName,
			initiatorName: initiator.initiatorName,
			authMethod: initiator.authMethod,
			chapName: initiator.chapName,
			chapSecret: initiator.chapSecret,
			tgtChapName: initiator.tgtChapName,
			tgtChapSecret: initiator.tgtChapSecret
		};
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
			if (form.tgtChapSecret.length < 12 || form.tgtChapSecret.length > 16) {
				toast.error('Target CHAP Secret must be 12-16 characters (RFC 3720)', {
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
		const response = await createInitiator(
			form.nickname,
			form.targetAddress,
			form.targetName,
			form.initiatorName,
			form.authMethod,
			form.chapName,
			form.chapSecret,
			form.tgtChapName,
			form.tgtChapSecret
		);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to create initiator', { position: 'bottom-center' });
			return;
		}
		toast.success('Initiator created', { position: 'bottom-center' });
		properties.create.open = false;
		reload = true;
	}

	async function submitEdit() {
		if (!activeRow) return;
		if (!validateChapSecrets()) return;
		loading = true;
		const response = await updateInitiator(
			Number(activeRow.id),
			form.nickname,
			form.targetAddress,
			form.targetName,
			form.initiatorName,
			form.authMethod,
			form.chapName,
			form.chapSecret,
			form.tgtChapName,
			form.tgtChapSecret
		);
		loading = false;
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to update initiator', { position: 'bottom-center' });
			return;
		}
		toast.success('Initiator updated', { position: 'bottom-center' });
		properties.edit.open = false;
		reload = true;
	}

	function generateTableData(
		initiators: ISCSIInitiator[],
		status: ISCSIStatus
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
			{ field: 'nickname', title: 'Nickname' },
			{ field: 'targetAddress', title: 'Target Address' },
			{ field: 'targetName', title: 'Target Name' },
			{ field: 'authMethod', title: 'Auth Method' },
			{
				field: 'createdAt',
				title: 'Created At',
				formatter: (cell) => convertDbTime(cell.getValue())
			}
		];

		const rows: Row[] = initiators.map((ini) => ({
			id: ini.id,
			nickname: ini.nickname,
			targetAddress: ini.targetAddress,
			targetName: ini.targetName,
			authMethod: ini.authMethod,
			connectionStatus: status[ini.targetName] ?? 'Not connected',
			createdAt: ini.createdAt
		}));

		return { rows, columns };
	}

	let tableData = $derived(generateTableData(initiators.current, status.current));
</script>

{#snippet initiatorForm(
	title: string,
	onSubmit: () => void,
	submitLabel: string,
	onClose: () => void
)}
	<Dialog.Header>
		<Dialog.Title>
			<SpanWithIcon icon="icon-[mdi--server-network]" size="h-5 w-5" gap="gap-2" {title} />
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
				<CustomValueInput
					label="Nickname"
					placeholder="fblock0"
					bind:value={form.nickname}
					classes="grid gap-1.5"
				/>
				<CustomValueInput
					label="Target Address"
					placeholder="192.168.1.10"
					bind:value={form.targetAddress}
					classes="grid gap-1.5"
				/>
				<div class="col-span-2">
					<CustomValueInput
						label="Target Name (IQN)"
						placeholder="iqn.2012-06.org.example:target1"
						bind:value={form.targetName}
						classes="grid gap-1.5"
					/>
				</div>
				<div class="col-span-2">
					<CustomValueInput
						label="Initiator Name (IQN, optional)"
						placeholder="iqn.2012-06.org.example.freebsd:nobody"
						bind:value={form.initiatorName}
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
								placeholder="inituser1"
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
								label="Target CHAP Name"
								placeholder="targetuser1"
								bind:value={form.tgtChapName}
								classes="grid gap-1.5"
							/>
							<CustomValueInput
								label="Target CHAP Secret"
								placeholder="Password (12-16 characters)"
								type="password"
								bind:value={form.tgtChapSecret}
								classes="grid gap-1.5"
								revealOnFocus={true}
							/>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<Dialog.Footer class="mt-4">
			<Button type="button" variant="outline" onclick={onClose} disabled={loading}>Cancel</Button>
			<Button type="submit" disabled={loading}>{submitLabel}</Button>
		</Dialog.Footer>
	</form>
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button onclick={openCreate} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{#if activeRows !== null && activeRows.length === 1}
			<Button onclick={openEdit} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit Initiator" />
			</Button>

			<Button
				onclick={() => (properties.delete.open = true)}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon
					icon="icon-[mdi--delete]"
					size="h-4 w-4"
					gap="gap-2"
					title="Delete Initiator"
				/>
			</Button>
		{/if}
	</div>

	<TreeTable
		data={tableData}
		name="iscsi-initiators-tt"
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
		{@render initiatorForm(
			'New iSCSI Initiator',
			submitCreate,
			'Create',
			() => (properties.create.open = false)
		)}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={properties.edit.open}>
	<Dialog.Content
		class="sm:max-w-145"
		showCloseButton={true}
		showResetButton={true}
		onClose={() => (properties.edit.open = false)}
		onReset={openEdit}
	>
		{@render initiatorForm(
			'Edit iSCSI Initiator',
			submitEdit,
			'Save',
			() => (properties.edit.open = false)
		)}
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={properties.delete.open}
	names={{ parent: 'iSCSI initiator', element: activeRow ? String(activeRow.nickname) : '' }}
	actions={{
		onConfirm: async () => {
			if (activeRow) {
				const response = await deleteInitiator(Number(activeRow.id));
				if (response.status === 'error') {
					handleAPIError(response);
					toast.error('Failed to delete initiator', { position: 'bottom-center' });
					return;
				}
				toast.success('Initiator deleted', { position: 'bottom-center' });
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

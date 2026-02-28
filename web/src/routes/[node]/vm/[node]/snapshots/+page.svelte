<script lang="ts">
	import {
		createVMSnapshot,
		deleteVMSnapshot,
		listVMSnapshots,
		rollbackVMSnapshot
	} from '$lib/api/vm/snapshots';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { VM } from '$lib/types/vm/vm';
	import type { VMSnapshot } from '$lib/types/vm/snapshots';
	import { renderWithIcon } from '$lib/utils/table';
	import { dateToAgo } from '$lib/utils/time';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';
	import { toast } from 'svelte-sonner';

	interface Data {
		rid: number;
		vm: VM;
		snapshots: VMSnapshot[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const snapshots = resource(
		() => `vm-${data.rid}-snapshots`,
		async (key) => {
			const result = await listVMSnapshots(data.rid);
			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.snapshots
		}
	);

	let reload = $state(false);
	watch(
		() => reload,
		(value) => {
			if (!value) return;
			snapshots.refetch();
			reload = false;
		}
	);

	function buildSnapshotTreeRows(items: VMSnapshot[]): Row[] {
		const sorted = [...items].sort((a, b) => {
			const left = new Date(a.createdAt).getTime();
			const right = new Date(b.createdAt).getTime();
			if (left === right) return a.id - b.id;
			return left - right;
		});

		const rowByID = new Map<number, Row>();
		for (const snap of sorted) {
			rowByID.set(snap.id, {
				id: snap.id,
				name: snap.name,
				description: snap.description || '-',
				snapshotName: snap.snapshotName,
				createdAt: snap.createdAt,
				createdLabel: dateToAgo(snap.createdAt),
				children: []
			});
		}

		const roots: Row[] = [];
		for (const snap of sorted) {
			const row = rowByID.get(snap.id);
			if (!row) continue;

			if (snap.parentSnapshotId && rowByID.has(snap.parentSnapshotId)) {
				const parent = rowByID.get(snap.parentSnapshotId);
				if (parent) {
					parent.children = parent.children || [];
					parent.children.push(row);
					continue;
				}
			}

			roots.push(row);
		}

		return roots;
	}

	let tableData = $derived({
		columns: [
			{
				field: 'name',
				title: 'Name',
				formatter: (cell: CellComponent) =>
					renderWithIcon('carbon:ibm-cloud-vpc-block-storage-snapshots', cell.getValue())
			},
			{
				field: 'description',
				title: 'Description'
			},
			{
				field: 'snapshotName',
				title: 'ZFS Snapshot Name',
				copyOnClick: true,
				visible: false
			},
			{
				field: 'createdLabel',
				title: 'Created',
				formatter: (cell: CellComponent) => {
					const rowData = cell.getRow().getData();
					return `<span title="${new Date(rowData.createdAt).toLocaleString()}">${cell.getValue()}</span>`;
				}
			}
		] as Column[],
		rows: buildSnapshotTreeRows(snapshots.current || [])
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let selectedSnapshot = $derived.by(() => {
		if (!activeRows || activeRows.length !== 1) return null;
		const id = Number(activeRows[0].id);
		return snapshots.current.find((snap) => snap.id === id) || null;
	});

	let createModal = $state({
		open: false,
		creating: false,
		name: '',
		description: ''
	});

	let rollbackConfirmOpen = $state(false);
	let deleteConfirmOpen = $state(false);
	let rollbacking = $state(false);

	async function onCreateSnapshot() {
		const name = createModal.name.trim();
		const description = createModal.description.trim();
		if (!name) {
			toast.error('Snapshot name is required', { position: 'bottom-center' });
			return;
		}

		createModal.creating = true;
		try {
			const response = await createVMSnapshot(data.rid, name, description);
			if (response.status === 'success') {
				toast.success('Snapshot created', { position: 'bottom-center' });
				createModal.open = false;
				createModal.name = '';
				createModal.description = '';
				reload = true;
				return;
			}

			handleAPIError(response);
			toast.error('Failed to create snapshot', { position: 'bottom-center' });
		} catch (e: any) {
			toast.error(e?.message || 'Failed to create snapshot', { position: 'bottom-center' });
		} finally {
			createModal.creating = false;
		}
	}

	async function onRollbackSnapshot() {
		if (!selectedSnapshot || rollbacking) return;
		rollbacking = true;
		try {
			const response = await rollbackVMSnapshot(data.rid, selectedSnapshot.id);
			if (response.status === 'success') {
				toast.success('Snapshot rollback started', { position: 'bottom-center' });
				rollbackConfirmOpen = false;
				activeRows = null;
				reload = true;
				return;
			}

			handleAPIError(response);
			toast.error('Failed to rollback snapshot', { position: 'bottom-center' });
		} catch (e: any) {
			toast.error(e?.message || 'Failed to rollback snapshot', { position: 'bottom-center' });
		} finally {
			rollbacking = false;
		}
	}

	async function onDeleteSnapshot() {
		if (!selectedSnapshot) return;
		try {
			const response = await deleteVMSnapshot(data.rid, selectedSnapshot.id);
			if (response.status === 'success') {
				toast.success('Snapshot deleted', { position: 'bottom-center' });
				deleteConfirmOpen = false;
				activeRows = null;
				reload = true;
				return;
			}

			handleAPIError(response);
			toast.error('Failed to delete snapshot', { position: 'bottom-center' });
		} catch {
			toast.error('Failed to delete snapshot', { position: 'bottom-center' });
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				createModal.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>
				<span>New</span>
			</div>
		</Button>

		{#if selectedSnapshot}
			<Button
				onclick={() => {
					rollbackConfirmOpen = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--backup-restore] mr-1 h-4 w-4"></span>
					<span>Rollback</span>
				</div>
			</Button>

			<Button
				onclick={() => {
					deleteConfirmOpen = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>
					<span>Delete</span>
				</div>
			</Button>
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="vm-snapshots-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

<Dialog.Root bind:open={createModal.open}>
	<Dialog.Content class="max-h-[90vh] min-w-1/3 overflow-y-auto p-5">
		<Dialog.Header>
			<Dialog.Title>
				<div class="flex items-center gap-2">
					<span class="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots] mr-1 h-4 w-4"></span>
					<span>New VM Snapshot</span>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-2">
			<div class="space-y-1.5">
				<Label for="snapshot-name">Name</Label>
				<Input
					id="snapshot-name"
					placeholder="pre-upgrade"
					bind:value={createModal.name}
					disabled={createModal.creating}
				/>
			</div>

			<div class="space-y-1.5">
				<Label for="snapshot-description">Description</Label>
				<Textarea
					id="snapshot-description"
					placeholder="Optional note about why this snapshot was taken"
					bind:value={createModal.description}
					rows={5}
					disabled={createModal.creating}
				/>
			</div>
		</div>

		<Dialog.Footer>
			<Button
				variant="outline"
				disabled={createModal.creating}
				onclick={() => {
					createModal.open = false;
				}}
			>
				Cancel
			</Button>
			<Button disabled={createModal.creating} onclick={onCreateSnapshot}>
				{createModal.creating ? 'Creating...' : 'Create Snapshot'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<AlertDialog
	open={rollbackConfirmOpen}
	loading={rollbacking}
	loadingLabel="Rolling Back"
	confirmLabel="Continue"
	customTitle={selectedSnapshot
		? `Rollback to <b>${selectedSnapshot.name}</b>? This will destroy snapshots created after it.`
		: ''}
	actions={{
		onConfirm: onRollbackSnapshot,
		onCancel: () => {
			rollbackConfirmOpen = false;
		}
	}}
/>

<AlertDialog
	open={deleteConfirmOpen}
	customTitle={selectedSnapshot
		? `Delete snapshot <b>${selectedSnapshot.name}</b>? This action cannot be undone.`
		: ''}
	actions={{
		onConfirm: onDeleteSnapshot,
		onCancel: () => {
			deleteConfirmOpen = false;
		}
	}}
/>

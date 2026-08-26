<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import {
		createVMSnapshot,
		deleteVMSnapshot,
		listVMSnapshots,
		rollbackVMSnapshot
	} from '$lib/api/vm/snapshots';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { Textarea } from '$lib/components/ui/textarea/index.js';
	import { reload as apiReload } from '$lib/stores/api.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { VMSnapshot } from '$lib/types/vm/snapshots';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { dateToAgo } from '$lib/utils/time';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { removeStaleCacheByRID } from '$lib/utils/vm/vm';
	import { resource, watch } from 'runed';
	import { onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';
	import { toast } from 'svelte-sonner';
	import { SvelteMap } from 'svelte/reactivity';

	interface Data {
		rid: number;
		node: string;
		snapshots: VMSnapshot[];
		snapshotsError: APIResponse | null;
	}

	let { data }: { data: Data } = $props();

	const initialSnapshots = () => data.snapshots;
	let lastSnapshots = initialSnapshots();

	const snapshots = resource(
		() => 'vm-' + data.rid + '-snapshots',
		async (key) => {
			const result = await listVMSnapshots(data.rid, {
				hostname: data.node,
				preserveErrors: true
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastSnapshots;
			}

			lastSnapshots = result;
			await updateCache(key, result, data.node);
			return result;
		},
		{
			initialValue: initialSnapshots()
		}
	);

	onMount(() => {
		if (data.snapshotsError) handleAPIError(data.snapshotsError);
	});

	let refetchSnapshots = $state(false);
	watch(
		() => refetchSnapshots,
		(value) => {
			if (!value) return;
			snapshots.refetch();
			refetchSnapshots = false;
		}
	);

	function buildSnapshotTreeRows(items: VMSnapshot[]): Row[] {
		const sorted = [...items].sort((a, b) => {
			const left = new Date(a.createdAt).getTime();
			const right = new Date(b.createdAt).getTime();
			if (left === right) return a.id - b.id;
			return left - right;
		});

		const rowByID = new SvelteMap<number, Row>();
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
	let deleting = $state(false);

	async function onCreateSnapshot() {
		const name = createModal.name.trim();
		const description = createModal.description.trim();
		if (!name) {
			toast.error('Snapshot name is required', { position: 'bottom-center' });
			return;
		}
		if (name.length > 128) {
			toast.error('Snapshot name must be 128 characters or fewer', {
				position: 'bottom-center'
			});
			return;
		}
		if (description.length > 4096) {
			toast.error('Snapshot description must be 4096 characters or fewer', {
				position: 'bottom-center'
			});
			return;
		}

		createModal.creating = true;
		try {
			const response = await createVMSnapshot(data.rid, name, description, {
				hostname: data.node,
				preserveErrors: true
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to create snapshot', { position: 'bottom-center' });
				return;
			}

			toast.success('Snapshot created', { position: 'bottom-center' });
			createModal.open = false;
			createModal.name = '';
			createModal.description = '';
			refetchSnapshots = true;
		} catch {
			toast.error('Failed to create snapshot', { position: 'bottom-center' });
		} finally {
			createModal.creating = false;
		}
	}

	async function onRollbackSnapshot() {
		if (!selectedSnapshot || rollbacking) return;
		rollbacking = true;
		try {
			const response = await rollbackVMSnapshot(data.rid, selectedSnapshot.id, {
				hostname: data.node,
				preserveErrors: true
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to rollback snapshot', { position: 'bottom-center' });
				return;
			}

			if (response.warnings.length > 0) {
				toast.warning('Snapshot rolled back with warnings', {
					position: 'bottom-center',
					description: response.warnings.join('\n')
				});
			} else {
				toast.success('Snapshot rolled back', { position: 'bottom-center' });
			}
			rollbackConfirmOpen = false;
			activeRows = null;
			await removeStaleCacheByRID(data.rid, data.node);
			apiReload.leftPanel = true;
			await invalidateAll();
			refetchSnapshots = true;
		} catch {
			toast.error('Failed to rollback snapshot', { position: 'bottom-center' });
		} finally {
			rollbacking = false;
		}
	}

	async function onDeleteSnapshot() {
		if (!selectedSnapshot || deleting) return;
		deleting = true;
		try {
			const response = await deleteVMSnapshot(data.rid, selectedSnapshot.id, {
				hostname: data.node,
				preserveErrors: true
			});
			if (response.status === 'success') {
				toast.success('Snapshot deleted', { position: 'bottom-center' });
				deleteConfirmOpen = false;
				activeRows = null;
				refetchSnapshots = true;
				return;
			}

			handleAPIError(response);
			toast.error('Failed to delete snapshot', { position: 'bottom-center' });
		} catch {
			toast.error('Failed to delete snapshot', { position: 'bottom-center' });
		} finally {
			deleting = false;
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
			disabled={createModal.creating || rollbacking || deleting}
			size="sm"
			class="h-6"
		>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-1" title="New" />
		</Button>

		{#if selectedSnapshot}
			<Button
				onclick={() => {
					rollbackConfirmOpen = true;
				}}
				size="sm"
				variant="outline"
				disabled={rollbacking || deleting}
				class="h-6.5"
			>
				<SpanWithIcon
					icon="icon-[mdi--backup-restore]"
					size="h-4 w-4"
					gap="gap-1"
					title="Rollback"
				/>
			</Button>

			<Button
				onclick={() => {
					deleteConfirmOpen = true;
				}}
				size="sm"
				variant="outline"
				disabled={rollbacking || deleting}
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-1" title="Delete" />
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
	<Dialog.Content
		class="min-w-1/3"
		showCloseButton={true}
		onClose={() => {
			createModal.open = false;
		}}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[carbon--ibm-cloud-vpc-block-storage-snapshots]"
					size="h-5 w-5"
					gap="gap-2"
					title="New VM Snapshot"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-0">
			<div class="space-y-1.5">
				<Label for="snapshot-name">Name</Label>
				<Input
					id="snapshot-name"
					placeholder="Clean Slate"
					bind:value={createModal.name}
					maxlength={128}
					disabled={createModal.creating}
				/>
			</div>

			<div class="space-y-1.5">
				<Label for="snapshot-description">Description</Label>
				<Textarea
					id="snapshot-description"
					placeholder="Optional note about why this snapshot was taken"
					bind:value={createModal.description}
					maxlength={4096}
					rows={5}
					disabled={createModal.creating}
				/>
			</div>
			<p class="text-muted-foreground text-xs">
				VM snapshots are crash-consistent, do not quiesce the guest, and are captured sequentially
				when storage spans multiple pools.
			</p>
		</div>

		<Dialog.Footer>
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
	confirmLabel="Rollback"
	customTitle={selectedSnapshot
		? `Rollback VM to <b>${escapeHTML(selectedSnapshot.name)}</b>? If the VM is running, it will be stopped. Every VM ZFS dataset will be rolled back, permanently destroying all newer ZFS snapshots under those datasets—including snapshots not listed here. The saved configuration will be restored and the VM will then be restarted if it was previously running.`
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
	loading={deleting}
	loadingLabel="Deleting"
	confirmLabel="Delete"
	customTitle={selectedSnapshot
		? `Delete snapshot <b>${escapeHTML(selectedSnapshot.name)}</b>? This removes it from every VM ZFS root and cannot be undone.`
		: ''}
	actions={{
		onConfirm: onDeleteSnapshot,
		onCancel: () => {
			deleteConfirmOpen = false;
		}
	}}
/>

<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import {
		createJailSnapshot,
		deleteJailSnapshot,
		listJailSnapshots,
		rollbackJailSnapshot
	} from '$lib/api/jail/snapshots';
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
	import { JailSnapshotRollbackResultSchema, type JailSnapshot } from '$lib/types/jail/snapshots';
	import { removeStaleJailCacheByCTID } from '$lib/utils/jail/jail';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { dateToAgo } from '$lib/utils/time';
	import { handleAPIError, isAPIResponse, removeCache, updateCache } from '$lib/utils/http';
	import { resource, watch } from 'runed';
	import { onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';
	import { toast } from 'svelte-sonner';
	import { SvelteMap } from 'svelte/reactivity';

	interface Data {
		node: string;
		ctId: number;
		snapshots: JailSnapshot[];
		snapshotsError: APIResponse | null;
	}
	interface SnapshotResourceValue {
		identity: string;
		items: JailSnapshot[];
	}

	let { data }: { data: Data } = $props();

	const initialSnapshots = () => data.snapshots;
	const initialSnapshotIdentity = () => `${data.node}:${data.ctId}`;
	let lastSnapshotIdentity = '';
	let lastSnapshots: JailSnapshot[] = [];

	const snapshots = resource(
		[() => data.node, () => data.ctId],
		async ([hostname, ctId], _, { signal }) => {
			const requestIdentity = `${hostname}:${ctId}`;
			if (lastSnapshotIdentity !== requestIdentity) {
				lastSnapshotIdentity = requestIdentity;
				lastSnapshots = data.node === hostname && data.ctId === ctId ? data.snapshots : [];
			}

			const result = await listJailSnapshots(ctId, {
				hostname,
				signal,
				preserveErrors: true
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return { identity: requestIdentity, items: lastSnapshots };
			}

			lastSnapshots = result;
			await updateCache(`jail-${ctId}-snapshots`, result, hostname);
			return { identity: requestIdentity, items: result };
		},
		{
			initialValue: {
				identity: initialSnapshotIdentity(),
				items: initialSnapshots()
			} satisfies SnapshotResourceValue
		}
	);
	let currentSnapshots = $derived(
		snapshots.current.identity === `${data.node}:${data.ctId}`
			? snapshots.current.items
			: data.snapshots
	);

	onMount(() => {
		if (data.snapshotsError) handleAPIError(data.snapshotsError);
	});

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (!value) return;
			snapshots.refetch();
			reload = false;
		}
	);

	function buildSnapshotTreeRows(items: JailSnapshot[]): Row[] {
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
					renderWithIcon(
						'carbon:ibm-cloud-vpc-block-storage-snapshots',
						escapeHTML(String(cell.getValue()))
					)
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
					const data = cell.getRow().getData();
					return `<span title="${new Date(data.createdAt).toLocaleString()}">${cell.getValue()}</span>`;
				}
			}
		] as Column[],
		rows: buildSnapshotTreeRows(currentSnapshots)
	});

	let query = $state('');
	let activeRows: Row[] | null = $state(null);
	let selectedSnapshot = $derived.by(() => {
		if (!activeRows || activeRows.length !== 1) return null;
		const id = Number(activeRows[0].id);
		return currentSnapshots.find((snap) => snap.id === id) || null;
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
		if (createModal.creating) return;
		const hostname = data.node;
		const ctId = data.ctId;
		const requestIdentity = `${hostname}:${ctId}`;
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
			const response = await createJailSnapshot(ctId, name, description, {
				hostname,
				preserveErrors: true
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				toast.error('Failed to create snapshot', { position: 'bottom-center' });
				return;
			}

			toast.success('Snapshot created', { position: 'bottom-center' });
			if (
				`${data.node}:${data.ctId}` === requestIdentity &&
				lastSnapshotIdentity === requestIdentity
			) {
				lastSnapshots = [...lastSnapshots, response];
				await updateCache(`jail-${ctId}-snapshots`, lastSnapshots, hostname);
				reload = true;
			} else {
				await removeCache(`jail-${ctId}-snapshots`, hostname);
			}
			createModal.open = false;
			createModal.name = '';
			createModal.description = '';
		} catch {
			toast.error('Failed to create snapshot', { position: 'bottom-center' });
		} finally {
			createModal.creating = false;
		}
	}

	async function onRollbackSnapshot() {
		if (!selectedSnapshot || rollbacking) return;
		const hostname = data.node;
		const ctId = data.ctId;
		const requestIdentity = `${hostname}:${ctId}`;
		const rollbackTarget = selectedSnapshot;
		rollbacking = true;
		try {
			const response = await rollbackJailSnapshot(ctId, rollbackTarget.id, {
				hostname,
				preserveErrors: true
			});
			if (isAPIResponse(response)) {
				handleAPIError(response);
				const diagnostics = JailSnapshotRollbackResultSchema.safeParse(response.data);
				toast.error('Failed to rollback snapshot', {
					position: 'bottom-center',
					description:
						diagnostics.success && diagnostics.data.warnings.length > 0
							? diagnostics.data.warnings.join('\n')
							: undefined
				});
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
			if (
				`${data.node}:${data.ctId}` === requestIdentity &&
				lastSnapshotIdentity === requestIdentity
			) {
				const rollbackCreatedAt = new Date(rollbackTarget.createdAt).getTime();
				lastSnapshots = lastSnapshots.filter((snapshot) => {
					const createdAt = new Date(snapshot.createdAt).getTime();
					return (
						createdAt < rollbackCreatedAt ||
						(createdAt === rollbackCreatedAt && snapshot.id <= rollbackTarget.id)
					);
				});
				reload = true;
			}
			await removeStaleJailCacheByCTID(ctId, hostname);
			apiReload.leftPanel = true;
			if (`${data.node}:${data.ctId}` === requestIdentity) await invalidateAll();
		} catch {
			toast.error('Failed to rollback snapshot', { position: 'bottom-center' });
		} finally {
			rollbacking = false;
		}
	}

	async function onDeleteSnapshot() {
		if (!selectedSnapshot || deleting) return;
		const hostname = data.node;
		const ctId = data.ctId;
		const requestIdentity = `${hostname}:${ctId}`;
		const deletedSnapshot = selectedSnapshot;
		deleting = true;
		try {
			const response = await deleteJailSnapshot(ctId, deletedSnapshot.id, {
				hostname,
				preserveErrors: true
			});
			if (response.status === 'success') {
				toast.success('Snapshot deleted', { position: 'bottom-center' });
				if (
					`${data.node}:${data.ctId}` === requestIdentity &&
					lastSnapshotIdentity === requestIdentity
				) {
					lastSnapshots = lastSnapshots
						.filter((snapshot) => snapshot.id !== deletedSnapshot.id)
						.map((snapshot) =>
							snapshot.parentSnapshotId === deletedSnapshot.id
								? { ...snapshot, parentSnapshotId: deletedSnapshot.parentSnapshotId }
								: snapshot
						);
					await updateCache(`jail-${ctId}-snapshots`, lastSnapshots, hostname);
					reload = true;
				} else {
					await removeCache(`jail-${ctId}-snapshots`, hostname);
				}
				deleteConfirmOpen = false;
				activeRows = null;
				return;
			} else {
				handleAPIError(response);
				toast.error('Failed to delete snapshot', { position: 'bottom-center' });
			}
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
			name="jail-snapshots-tt"
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
					title="New Jail Snapshot"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-4 py-2">
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
				Jail snapshots are crash-consistent and do not quiesce processes running inside the jail.
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
		? `Rollback jail to <b>${escapeHTML(selectedSnapshot.name)}</b>? If the jail is running, it will be stopped. The complete jail dataset tree will be rolled back, permanently destroying every newer ZFS snapshot beneath it—including snapshots not listed here. Saved configuration will be restored and the jail will then be restarted if it was previously running.`
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
		? `Delete snapshot <b>${escapeHTML(selectedSnapshot.name)}</b>? This removes it throughout the jail dataset tree and cannot be undone.`
		: ''}
	actions={{
		onConfirm: onDeleteSnapshot,
		onCancel: () => {
			deleteConfirmOpen = false;
		}
	}}
/>

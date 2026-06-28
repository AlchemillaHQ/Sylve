<script lang="ts">
	import { storage } from '$lib';
	import { setTunable } from '$lib/api/system/tunables';
	import SingleValueDialog from '$lib/components/custom/Dialog/SingleValue.svelte';
	import ValueViewer from '$lib/components/custom/Dialog/ValueViewer.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import TreeTable from '$lib/components/custom/TreeTableRemote.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { handleAPIError } from '$lib/utils/http';
	import { sha256 } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	let hash = $state('');
	let query = $state('');
	let reload = $state(false);
	let loading = $state(false);
	let activeRows = $state<Row[] | null>(null);

	let activeRow: Row | null = $derived(
		activeRows && activeRows.length === 1 ? (activeRows[0] as Row) : null
	);
	let canEdit = $derived(activeRow ? activeRow.writable === true : false);
	let canView = $derived.by(() => {
		if (!activeRow) return false;
		const value = activeRow.value;
		if (value === null || value === undefined) return false;
		const trimmed = String(value).trim();
		return trimmed !== '' && trimmed !== '-';
	});

	let editModal = $state({ open: false, name: '' });
	let editValue = $state('');

	let viewModal = $state({ open: false, name: '', value: '' });

	const toastOpts = {
		duration: 5000,
		position: 'bottom-center' as const
	};

	onMount(async () => {
		hash = await sha256(storage.token || '', 1);
	});

	const dashIfEmpty = (cell: CellComponent) => {
		const value = cell.getValue();
		return value === null || value === undefined || value === '' ? '-' : String(value);
	};

	let columns: Column[] = $derived([
		{ field: 'name', title: 'Name' },
		{ field: 'value', title: 'Value', formatter: dashIfEmpty },
		{ field: 'type', title: 'Type', formatter: dashIfEmpty },
		{
			field: 'writable',
			title: 'Writable',
			formatter: (cell: CellComponent) =>
				cell.getValue()
					? renderWithIcon('mdi:check-circle-outline', 'Yes', 'text-green-500')
					: renderWithIcon('mdi:lock-outline', 'No', 'text-muted-foreground')
		}
	]);

	let tableData = $derived({ rows: [], columns });

	function openEdit() {
		if (!activeRow) return;
		editModal.name = String(activeRow.name);
		editValue = String(activeRow.value ?? '');
		editModal.open = true;
	}

	function openView() {
		if (!activeRow) return;
		viewModal.name = String(activeRow.name);
		viewModal.value = String(activeRow.value ?? '');
		viewModal.open = true;
	}

	async function save() {
		if (!editModal.name) return;

		loading = true;
		const res = await setTunable(editModal.name, editValue);
		loading = false;

		if (res.status === 'success') {
			toast.success(`Tunable ${editModal.name} updated`, toastOpts);
			editModal.open = false;
			activeRows = null;
			reload = true;
		} else {
			handleAPIError(res);
			toast.error(`Failed to update ${editModal.name}`, toastOpts);
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		{#if canView}
			<Button size="sm" variant="outline" class="h-6.5 shrink-0" onclick={openView}>
				<div class="flex items-center">
					<span class="icon-[mdi--eye-outline] mr-1 h-4 w-4"></span>
					<span>View</span>
				</div>
			</Button>
		{/if}

		{#if canEdit}
			<Button size="sm" variant="outline" class="h-6.5 shrink-0" onclick={openEdit}>
				<div class="flex items-center">
					<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
					<span>Edit {activeRow?.name}</span>
				</div>
			</Button>
		{/if}

		<Button
			onclick={() => (reload = true)}
			size="sm"
			variant="outline"
			class="ml-auto h-6 shrink-0"
		>
			<div class="flex items-center">
				<span class="icon-[mdi--refresh] h-4 w-4"></span>
			</div>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		{#if hash}
			<TreeTable
				data={tableData}
				name="system-tunables-tt"
				ajaxURL="/api/system/tunables/remote?hash={hash}"
				bind:query
				bind:parentActiveRow={activeRows}
				bind:reload
				multipleSelect={false}
				initialSort={[{ column: 'name', dir: 'asc' }]}
			/>
		{/if}
	</div>
</div>

<SingleValueDialog
	bind:open={editModal.open}
	title={`Edit ${editModal.name}`}
	type="text"
	placeholder="Enter value"
	bind:value={editValue}
	onSave={save}
	bind:loading
/>

<ValueViewer bind:open={viewModal.open} title={viewModal.name} value={viewModal.value} />

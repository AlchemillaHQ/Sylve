<script lang="ts">
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Download } from '$lib/types/utilities/downloader';
	import type { VM } from '$lib/types/vm/vm';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import type { Zpool } from '$lib/types/zfs/pool';
	import AddStorageDialog from './Storage/AddStorageDialog.svelte';
	import EditStorageDialog from './Storage/EditStorageDialog.svelte';

	interface Props {
		open: boolean;
		node: string;
		datasets: Dataset[];
		downloads: Download[];
		vm: VM;
		vms: VM[];
		pools: Zpool[];
		storageId: number | null;
		tableData: { rows: Row[]; columns: Column[] } | null;
		reload: boolean;
	}

	let {
		open = $bindable(),
		node,
		datasets,
		downloads,
		vm,
		vms,
		pools,
		storageId,
		tableData,
		reload = $bindable()
	}: Props = $props();
</script>

{#if storageId === null}
	<AddStorageDialog bind:open {node} {datasets} {downloads} {vm} {vms} {pools} bind:reload />
{:else}
	<EditStorageDialog
		bind:open
		{node}
		{datasets}
		{downloads}
		{vm}
		{storageId}
		{tableData}
		bind:reload
	/>
{/if}

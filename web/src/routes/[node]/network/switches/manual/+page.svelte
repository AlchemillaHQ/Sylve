<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { deleteManualSwitch, getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Create from '$lib/components/custom/Network/Switch/Manual/Create.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/network/switch/manual';
	import { createQueries } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
	}

	let { data }: { data: Data } = $props();

	const results = createQueries(() => ({
		queries: [
			{
				queryKey: ['network-interfaces'],
				queryFn: async () => {
					return await getInterfaces();
				},
				keepPreviousData: true,
				initialData: data.interfaces,
				onSuccess: (data: Iface[]) => {
					updateCache('network-interfaces', data);
				}
			},
			{
				queryKey: ['network-switches'],
				queryFn: async () => {
					return await getSwitches();
				},
				keepPreviousData: true,
				initialData: data.switches,
				onSuccess: (data: SwitchList) => {
					updateCache('network-switches', data);
				}
			}
		]
	}));

	const interfaces = $derived(results[0].data);
	const switches = $derived(results[1].data);
	const usable = $derived.by(() => {
		const result: string[] = [];
		const ifaces = interfaces ? interfaces.filter((iface) => iface.groups?.includes('bridge')) : [];
		if (!ifaces.length) return [];

		const standard = switches ? switches['standard'] || [] : [];
		const manual = switches ? switches['manual'] || [] : [];

		for (const iface of ifaces) {
			const usedInStandard = standard.some((sw) => sw.bridgeName === iface.name);
			const usedInManual = manual.some((sw) => sw.bridge === iface.name);

			if (!usedInStandard && !usedInManual) {
				result.push(iface.name);
			}
		}

		return result;
	});

	let tableData = $derived(generateTableData(switches));
	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query: string = $state('');

	function reloadData() {
		results.forEach((result) => {
			result.refetch();
		});
	}

	let reload = $state(false);
	$effect(() => {
		if (reload) {
			reloadData();
			reload = false;
		}
	});

	let modals = $state({
		newSwitch: {
			open: false
		},
		deleteSwitch: {
			open: false,
			name: '',
			id: 0
		}
	});

	function handleDelete() {
		if (activeRow && Object.keys(activeRow).length > 0) {
			modals.deleteSwitch.open = true;
			modals.deleteSwitch.name = activeRow.name;
			modals.deleteSwitch.id = activeRow.id as number;
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			onclick={() => {
				if (usable && usable.length === 0) {
					toast.error('No usable bridges available', {
						position: 'bottom-center'
					});
				} else {
					modals.newSwitch.open = true;
				}
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{#if activeRow && Object.keys(activeRow).length > 0}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<div class="flex items-center">
					<span class="icon-[mdi--delete] mr-1 h-4 w-4"></span>

					<span>Delete</span>
				</div>
			</Button>
		{/if}
	</div>

	<TreeTable
		name="tt-switches"
		data={tableData}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

<Create bind:open={modals.newSwitch.open} bridges={usable || []} bind:reload />

<AlertDialog
	open={modals.deleteSwitch.open}
	names={{ parent: 'switch', element: modals.deleteSwitch.name }}
	actions={{
		onConfirm: async () => {
			const result = await deleteManualSwitch(modals.deleteSwitch.id);
			reloadData();
			if (isAPIResponse(result) && result.status === 'success') {
				toast.success(`Switch ${modals.deleteSwitch.name} deleted`, {
					position: 'bottom-center'
				});
			} else {
				if (result && result.error) {
					if (result.error === 'switch_in_use_by_vm') {
						toast.error('Switch is in use by a VM', { position: 'bottom-center' });
					} else {
						toast.error('Error deleting switch', { position: 'bottom-center' });
					}
				}
			}

			modals.deleteSwitch.open = false;
			modals.deleteSwitch.name = '';
			modals.deleteSwitch.id = 0;
			activeRows = null;
		},
		onCancel: () => {
			modals.deleteSwitch.open = false;
			modals.deleteSwitch.name = '';
			modals.deleteSwitch.id = 0;
		}
	}}
></AlertDialog>

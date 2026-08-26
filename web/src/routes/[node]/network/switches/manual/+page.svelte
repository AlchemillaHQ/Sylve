<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { deleteManualSwitch, getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Create from '$lib/components/custom/Network/Switch/Manual/Create.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Iface } from '$lib/types/network/iface';
	import {
		emptySwitchList,
		isSwitchList,
		type ManualSwitchRow,
		type SwitchList
	} from '$lib/types/network/switch';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateTableData } from '$lib/utils/network/switch/manual';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';

	interface Data {
		interfaces: Iface[] | APIResponse;
		switches: SwitchList | APIResponse;
	}

	let { data }: { data: Data } = $props();
	// svelte-ignore state_referenced_locally
	let lastGoodInterfaces = Array.isArray(data.interfaces) ? data.interfaces : ([] as Iface[]);
	// svelte-ignore state_referenced_locally
	let lastGoodSwitches = isSwitchList(data.switches) ? data.switches : emptySwitchList();

	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			if (isAPIResponse(res)) {
				handleAPIError(res);
				return lastGoodInterfaces;
			}
			lastGoodInterfaces = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodInterfaces }
	);

	let networkSwitches = resource(
		() => 'network-switches',
		async (key) => {
			const res = await getSwitches();
			if (!isSwitchList(res)) {
				handleAPIError(res);
				return lastGoodSwitches;
			}
			lastGoodSwitches = res;
			updateCache(key, res);
			return res;
		},
		{ initialValue: lastGoodSwitches }
	);

	const usable = $derived.by(() => {
		const result: string[] = [];
		const ifaces = networkInterfaces.current
			? networkInterfaces.current.filter((iface) => iface.groups?.includes('bridge'))
			: [];
		if (!ifaces.length) return [];

		const standard = networkSwitches.current ? networkSwitches.current['standard'] || [] : [];
		const manual = networkSwitches.current ? networkSwitches.current['manual'] || [] : [];
		for (const iface of ifaces) {
			const usedInStandard = standard.some((sw) => sw.bridgeName === iface.name);
			const usedInManual = manual.some((sw) => sw.bridge === iface.name);

			if (!usedInStandard && !usedInManual) {
				result.push(iface.name);
			}
		}

		return result;
	});

	let tableData = $derived(generateTableData(networkSwitches.current));
	let activeRows: ManualSwitchRow[] | null = $state(null);
	let activeRow: ManualSwitchRow | null = $derived(
		activeRows ? (activeRows[0] as ManualSwitchRow) : ({} as ManualSwitchRow)
	);
	let query: string = $state('');

	let reload = $state(false);
	watch(
		() => reload,
		(current) => {
			if (current) {
				networkInterfaces.refetch();
				networkSwitches.refetch();
				reload = false;
			}
		}
	);

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

	function deleteErrorMessage(error: APIResponse['error']): string {
		if (typeof error !== 'string') return 'Error deleting switch';
		switch (error) {
			case 'manual_switch_in_use_by_vm':
				return 'Switch is in use by a VM';
			case 'manual_switch_in_use_by_jail':
				return 'Switch is in use by a jail';
			case 'manual_switch_in_use_by_dhcp_config':
				return 'Switch is enabled in the DHCP configuration';
			case 'manual_switch_in_use_by_dhcp_range':
				return 'Switch is in use by a DHCP range';
			case 'manual_switch_not_found':
				return 'Switch no longer exists';
			default:
				return 'Error deleting switch';
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
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{#if activeRow && Object.keys(activeRow).length > 0}
			<Button onclick={handleDelete} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
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
	keepOpenOnConfirm={true}
	names={{ parent: 'switch', element: modals.deleteSwitch.name }}
	actions={{
		onConfirm: async () => {
			const result = await deleteManualSwitch(modals.deleteSwitch.id);
			if (result.status !== 'success') {
				handleAPIError(result);
				toast.error(deleteErrorMessage(result.error), { position: 'bottom-center' });
				return;
			}

			toast.success(`Switch ${modals.deleteSwitch.name} deleted`, {
				position: 'bottom-center'
			});
			reload = true;
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

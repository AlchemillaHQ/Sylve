<script lang="ts">
	import { getDHCPConfig } from '$lib/api/network/dhcp';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import Config from '$lib/components/custom/Network/DHCP/Config.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { DHCPConfig } from '$lib/types/network/dhcp';
	import type { Iface } from '$lib/types/network/iface';
	import type { ManualSwitch, StandardSwitch, SwitchList } from '$lib/types/network/switch';
	import { updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		interfaces: Iface[];
		switches: SwitchList;
		dhcpConfig: DHCPConfig;
	}

	let { data }: { data: Data } = $props();

	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key, prevKey, { signal }) => {
			const res = await getInterfaces();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	let networkSwitches = resource(
		() => 'network-switches',
		async (key, prevKey, { signal }) => {
			const res = await getSwitches();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.switches }
	);

	let dhcpConfig = resource(
		() => 'dhcp-config',
		async (key, prevKey, { signal }) => {
			const res = await getDHCPConfig();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.dhcpConfig }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
				networkInterfaces.refetch();
				networkSwitches.refetch();
				dhcpConfig.refetch();
				reload = false;
			}
		}
	);

	let query = $state('');
	let tableData = $derived.by(() => {
		const columns: Column[] = [
			{
				field: 'property',
				title: 'Property'
			},
			{
				field: 'value',
				title: 'Value',
				formatter: (cell: CellComponent) => {
					const property = cell.getRow().getData().property;
					if (property === 'DNS Servers') {
						const values = cell.getValue() as string[];
						if (values.length > 3) {
							return `<div class="whitespace-pre-wrap">${values.slice(0, 3).join('<br>')}<br>...</div>`;
						} else {
							return `<div class="whitespace-pre-wrap">${values.join('<br>')}</div>`;
						}
					}

					if (property === 'Switches') {
						const switches = cell.getValue() as {
							standard: StandardSwitch[];
							manual: ManualSwitch[];
						};

						if (switches.standard.length === 0 && switches.manual.length === 0) {
							return '-';
						}

						const standard = switches.standard.map((s) => s.name);
						const manual = switches.manual.map((s) => s.name);

						return `<div class="whitespace-pre-wrap">${[...standard, ...manual].join('<br>')}</div>`;
					}

					return cell.getValue();
				}
			}
		];

		const rows: Row[] = [
			{
				id: generateNanoId('id'),
				property: 'Domain',
				value: dhcpConfig.current.domain
			},
			{
				id: generateNanoId('expandHosts'),
				property: 'Expand Hosts',
				value: dhcpConfig.current.expandHosts ? 'Yes' : 'No'
			},
			{
				id: generateNanoId('dnsServers'),
				property: 'DNS Servers',
				value: dhcpConfig.current.dnsServers
			},
			{
				id: generateNanoId('switches'),
				property: 'Switches',
				value: {
					standard: dhcpConfig.current.standardSwitches,
					manual: dhcpConfig.current.manualSwitches
				}
			}
		];

		return {
			rows,
			columns
		};
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let modalOpen = $state(false);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" variant="default" class="h-6.5" onclick={() => (modalOpen = true)}>
			<span class="icon-[hugeicons--system-update-01] h-4 w-4"></span>
			{'Update'}
		</Button>
	</div>

	<TreeTable
		name="tt-dhcp-config"
		data={tableData}
		bind:parentActiveRow={activeRows}
		multipleSelect={false}
	/>
</div>

<Config
	bind:open={modalOpen}
	bind:reload
	networkInterfaces={networkInterfaces.current}
	networkSwitches={networkSwitches.current}
	dhcpConfig={dhcpConfig.current}
/>

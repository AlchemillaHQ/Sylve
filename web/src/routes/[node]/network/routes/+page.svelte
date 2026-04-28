<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { deleteStaticRoute, getStaticRoutes } from '$lib/api/network/route';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Form from '$lib/components/custom/Network/Routes/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { StaticRoute } from '$lib/types/network/route';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { NetworkObject } from '$lib/types/network/object';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { getFriendlyName } from '$lib/utils/network/helpers';
	import { isBoolean } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		routes: StaticRoute[] | APIResponse;
		interfaces: Iface[];
		switches: SwitchList;
		objects: NetworkObject[] | APIResponse;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const routes = resource(
		() => 'network-static-routes',
		async (key) => {
			const result = await getStaticRoutes();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [];
			}

			updateCache(key, result);
			return result;
		},
		{
			initialValue: Array.isArray(data.routes) ? data.routes : ([] as StaticRoute[])
		}
	);

	// svelte-ignore state_referenced_locally
	const interfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const result = await getInterfaces();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [];
			}

			updateCache(key, result);
			return result;
		},
		{
			initialValue: Array.isArray(data.interfaces) ? data.interfaces : ([] as Iface[])
		}
	);

	// svelte-ignore state_referenced_locally
	const switches = resource(
		() => 'network-switches',
		async (key) => {
			const result = await fetch('/api/network/switches').then((res) => res.json());
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return { standard: [], manual: [] };
			}

			updateCache(key, result);
			return result;
		},
		{
			initialValue: data.switches ?? { standard: [], manual: [] }
		}
	);

	// svelte-ignore state_referenced_locally
	const objects = resource(
		() => 'network-objects',
		async (key) => {
			const result = await getNetworkObjects();
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [] as NetworkObject[];
			}

			updateCache(key, result);
			return result;
		},
		{
			initialValue: Array.isArray(data.objects) ? data.objects : ([] as NetworkObject[])
		}
	);

	let modals = $state({
		create: { open: false },
		edit: { open: false, id: 0 },
		delete: { open: false, id: 0 }
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query: string = $state('');

	let columns: Column[] = $state([
		{ field: 'id', title: 'ID', visible: false },
		{
			field: 'enabled',
			title: 'Enabled',
			visible: false
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as Row;
				const enabled = isBoolean(row.enabled) && row.enabled === true;

				if (enabled) {
					return renderWithIcon(
						'lets-icons:check-fill',
						String(cell.getValue() ?? ''),
						'text-green-500'
					);
				} else {
					return renderWithIcon(
						'gridicons:cross-circle',
						String(cell.getValue() ?? ''),
						'text-red-400'
					);
				}
			}
		},
		{ field: 'fib', title: 'FIB' },
		{
			field: 'destination',
			title: 'Destination',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as Row;
				const kind = String(row.destinationType ?? '');
				const destination = String(cell.getValue() ?? '');
				if (kind === 'network') {
					return renderWithIcon('mdi:network', destination, 'text-blue-500');
				} else {
					return renderWithIcon('mdi:dns', destination, 'text-gray-500');
				}
			},
			tooltip: (_e: MouseEvent, cell: CellComponent) => {
				const row = cell.getRow().getData() as Row;
				const kind = String(row.destinationType ?? '');

				if (kind === 'network') {
					return `Network → ${String(cell.getValue() ?? '')}`;
				} else {
					return `Host → ${String(cell.getValue() ?? '')}`;
				}
			}
		},
		{
			field: 'nextHop',
			title: 'Next Hop',
			formatter: (cell: CellComponent) => {
				const row = cell.getRow().getData() as Row;
				const mode = String(row.nextHopMode ?? '');

				if (mode === 'gateway') {
					return renderWithIcon(
						'fluent:storage-20-filled',
						`${String(row.gateway ?? '-')}`,
						'text-yellow-500'
					);
				} else {
					return renderWithIcon(
						'mdi:lan',
						String(row.interfaceFriendlyName ?? row.interface ?? '-'),
						'text-purple-500'
					);
				}
			}
		},
		{
			field: 'family',
			title: 'Family',
			formatter: (cell: CellComponent) => {
				const value = String(cell.getValue() ?? 'inet');
				return value === 'inet6' ? 'IPv6' : 'IPv4';
			}
		}
	]);

	const tableData: { rows: Row[]; columns: Column[] } = $derived({
		columns,
		rows: routes.current.map((route) => ({
			id: route.id,
			name: route.name,
			enabled: route.enabled,
			fib: route.fib,
			destinationType: route.destinationType,
			destination: route.destination,
			nextHopMode: route.nextHopMode,
			gateway: route.gateway,
			interface: route.interface,
			interfaceFriendlyName:
				route.nextHopMode === 'interface'
					? getFriendlyName(route.interface, switches.current, interfaces.current)
					: '',
			nextHop:
				route.nextHopMode === 'gateway'
					? `gateway:${route.gateway}`
					: `interface:${route.interface}`,
			family: route.family,
			updatedAt: route.updatedAt
		}))
	});
</script>

{#snippet button(type: string)}
	{#if activeRows && activeRows.length == 1}
		{#if type === 'delete'}
			<Button
				onclick={() => {
					modals.delete.open = !modals.delete.open;
					modals.delete.id = Number(activeRow?.id);
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{:else if type === 'edit'}
			<Button
				onclick={() => {
					modals.edit.open = !modals.edit.open;
					modals.edit.id = Number(activeRow?.id);
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create.open = !modals.create.open)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	<TreeTable
		data={tableData}
		name="tt-network-routes"
		multipleSelect={false}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.create.open}
	<Form
		bind:open={modals.create.open}
		edit={false}
		routes={routes.current}
		interfaces={interfaces.current}
		objects={objects.current}
		switches={switches.current}
		afterChange={() => {
			routes.refetch();
		}}
	/>
{/if}

{#if modals.edit.open}
	<Form
		bind:open={modals.edit.open}
		edit={true}
		id={Number(modals.edit.id)}
		routes={routes.current}
		interfaces={interfaces.current}
		objects={objects.current}
		switches={switches.current}
		afterChange={() => {
			routes.refetch();
		}}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'static route', element: activeRow?.name || 'unknown' }}
	actions={{
		onConfirm: async () => {
			const result = await deleteStaticRoute(modals.delete.id);
			routes.refetch();
			if ('status' in result && result.status === 'success') {
				toast.success(`Route ${String(activeRow?.name ?? '')} deleted`, {
					position: 'bottom-center'
				});
			} else {
				handleAPIError(result);
				toast.error('Failed to delete route', { position: 'bottom-center' });
			}
		},
		onCancel: async () => {
			modals.delete.id = 0;
		}
	}}
/>

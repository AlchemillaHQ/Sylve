<script lang="ts">
	import {
		deleteFirewallTrafficRule,
		getFirewallTrafficRuleCounters,
		getFirewallTrafficRules,
		reorderFirewallTrafficRules,
		type FirewallReorderRequest
	} from '$lib/api/network/firewall';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Form from '$lib/components/custom/Network/Firewall/Traffic/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type {
		FirewallTrafficRule,
		FirewallTrafficRuleCounter
	} from '$lib/types/network/firewall';
	import type { Iface } from '$lib/types/network/iface';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { WireGuardClient } from '$lib/types/network/wireguard';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { convertDbTime } from '$lib/utils/time';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { onMount } from 'svelte';
	import type { CellComponent, RowComponent } from 'tabulator-tables';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import { SvelteMap } from 'svelte/reactivity';

	interface Data {
		trafficRules: FirewallTrafficRule[] | APIResponse;
		objects: NetworkObject[] | APIResponse;
		interfaces: Iface[];
		switches: SwitchList | APIResponse;
		wgClients: WireGuardClient[] | APIResponse;
	}

	let {
		data
	}: {
		data: Data;
	} = $props();

	// svelte-ignore state_referenced_locally
	const trafficRulesResource = resource(
		() => 'firewall-traffic-rules',
		async (key) => {
			const result = await getFirewallTrafficRules();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.trafficRules as FirewallTrafficRule[] }
	);

	const trafficRules = $derived(
		Array.isArray(trafficRulesResource.current)
			? (trafficRulesResource.current as FirewallTrafficRule[])
			: []
	);

	let counterFetchIntent: 'auto' | 'manual' = 'auto';
	let countersUpdating = $state(false);
	let lastGoodCounters: FirewallTrafficRuleCounter[] = [];

	const countersResource = resource(
		() => 'firewall-traffic-rule-counters',
		async (key) => {
			const result = await getFirewallTrafficRuleCounters();
			if (Array.isArray(result)) {
				lastGoodCounters = result;
				updateCache(key, result);
				return result;
			}

			if (counterFetchIntent === 'manual') {
				handleAPIError(result);
				toast.error('Failed to refresh traffic counters', { position: 'bottom-center' });
			}

			return lastGoodCounters;
		},
		{ initialValue: [] as FirewallTrafficRuleCounter[] }
	);

	const counters = $derived(
		Array.isArray(countersResource.current)
			? (countersResource.current as FirewallTrafficRuleCounter[])
			: []
	);

	const counterByRuleID = $derived.by(() => {
		const out = new SvelteMap<number, FirewallTrafficRuleCounter>();
		for (const counter of counters) {
			out.set(counter.id, counter);
		}
		return out;
	});

	// svelte-ignore state_referenced_locally
	const objectsResource = resource(
		() => 'network-objects',
		async (key) => {
			const result = await getNetworkObjects();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.objects as NetworkObject[] }
	);

	const objects = $derived(
		Array.isArray(objectsResource.current) ? (objectsResource.current as NetworkObject[]) : []
	);

	// svelte-ignore state_referenced_locally
	const interfacesResource = resource(
		() => 'network-ifaces',
		async (key) => {
			const result = await getInterfaces();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.interfaces }
	);

	const interfaces = $derived(
		Array.isArray(interfacesResource.current) ? (interfacesResource.current as Iface[]) : []
	);

	// svelte-ignore state_referenced_locally
	const switchesResource = resource(
		() => 'network-switches',
		async (key) => {
			const result = await getSwitches();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.switches as SwitchList }
	);

	const switches = $derived(
		switchesResource.current &&
			typeof switchesResource.current === 'object' &&
			!Array.isArray(switchesResource.current) &&
			'status' in switchesResource.current
			? { standard: [], manual: [] }
			: ((switchesResource.current as SwitchList) ?? { standard: [], manual: [] })
	);

	const wgClients = $derived(
		Array.isArray(data.wgClients) ? (data.wgClients as WireGuardClient[]) : []
	);

	let activeRow: Row[] | null = $state(null);
	let query: string = $state('');

	let modals = $state({
		create: { open: false },
		edit: { open: false, id: 0 },
		delete: { open: false }
	});

	function resolveInterfaceName(name: string): string {
		const stdSwitch = switches.standard?.find((sw) => sw.bridgeName === name);
		if (stdSwitch) return stdSwitch.name;
		const manSwitch = switches.manual?.find((sw) => sw.bridge === name);
		if (manSwitch) return manSwitch.name;
		const iface = interfaces.find((ifc) => ifc.name === name);
		if (iface?.description) return iface.description;
		return name;
	}

	function formatObjectName(id: number | null | undefined): string {
		if (!id) return 'Any';
		const object = objects.find((obj) => obj.id === id);
		if (!object) return `Object #${id}`;
		return object.name;
	}

	function resolvePortValue(
		raw: string | null | undefined,
		objId: number | null | undefined
	): string {
		if (raw) return raw;
		if (!objId) return '';
		const obj = objects.find((o) => o.id === objId);
		if (!obj) return '';
		return (obj.entries ?? []).map((e) => e.value).join(', ');
	}

	function formatEnabled(value: boolean): string {
		if (value) return renderWithIcon('mdi:check-circle', '', 'text-green-500');
		return renderWithIcon('mdi:close-circle', '', 'text-red-400');
	}

	function quickBadge(quick: boolean): string {
		if (!quick) return '';
		return '<span class="inline-flex items-center text-xs font-mono px-1 rounded border text-emerald-400 border-emerald-400/50 leading-tight">QUICK</span>';
	}

	function logBadge(log: boolean): string {
		if (!log) return '';
		return '<span class="inline-flex items-center text-xs font-mono px-1 rounded border text-amber-400 border-amber-400/50 leading-tight">LOG</span>';
	}

	function formatAction(action: string, direction: string, quick: boolean, log: boolean): string {
		const isPass = action === 'pass';
		const actionPart = renderWithIcon(
			isPass ? 'mdi:check-circle-outline' : 'mdi:close-octagon-outline',
			isPass ? 'Pass' : 'Block',
			isPass ? 'text-green-500' : 'text-red-400'
		);
		const dirPart = renderWithIcon(
			direction === 'in' ? 'mdi:arrow-down-circle-outline' : 'mdi:arrow-up-circle-outline',
			direction === 'in' ? 'In' : 'Out',
			direction === 'in' ? 'text-blue-400' : 'text-orange-400'
		);
		const qb = quickBadge(quick);
		const lb = logBadge(log);
		return `<span class="inline-flex items-center gap-1.5">${actionPart}<span class="text-muted-foreground/50">·</span>${dirPart}${qb ? '<span class="text-muted-foreground/50">·</span>' + qb : ''}${lb ? '<span class="text-muted-foreground/50">·</span>' + lb : ''}</span>`;
	}

	function familyBadge(family: string): string {
		if (!family || family === 'any') return '';
		const label = family === 'inet' ? 'IPv4' : 'IPv6';
		const color =
			family === 'inet'
				? 'text-blue-400 border-blue-400/50'
				: 'text-violet-400 border-violet-400/50';
		return `<span class="inline-flex items-center text-xs font-mono px-1 rounded border ${color} leading-tight">${label}</span>`;
	}

	function protoBadge(protocol: string): string {
		if (!protocol || protocol === 'any') return '';
		const colors: Record<string, string> = {
			tcp: 'text-cyan-400 border-cyan-400/50',
			udp: 'text-amber-400 border-amber-400/50',
			icmp: 'text-pink-400 border-pink-400/50'
		};
		const color = colors[protocol] || 'text-muted-foreground border-muted-foreground/50';
		return `<span class="inline-flex items-center text-xs font-mono px-1 rounded border ${color} leading-tight">${protocol.toUpperCase()}</span>`;
	}

	function formatEndpointParts(addr: string, isObj: boolean): string {
		if (!addr || addr === 'any') return renderWithIcon('mdi:earth', 'Any', 'text-sky-400');
		if (isObj) return renderWithIcon('mdi:tag-outline', addr, 'text-purple-400');
		return renderWithIcon('mdi:ip-network', addr, 'text-indigo-400');
	}

	function formatSource(addr: string, isObj: boolean, family: string, port: string): string {
		const parts: string[] = [];
		const fb = familyBadge(family);
		if (fb) parts.push(fb);
		parts.push(formatEndpointParts(addr, isObj));
		if (port) parts.push(renderWithIcon('mdi:pound', port, 'text-zinc-400'));
		return `<span class="inline-flex items-center gap-1.5">${parts.join('<span class="text-muted-foreground/40 text-xs">·</span>')}</span>`;
	}

	function formatDestination(addr: string, isObj: boolean, protocol: string, port: string): string {
		const parts: string[] = [];
		const pb = protoBadge(protocol);
		if (pb) parts.push(pb);
		parts.push(formatEndpointParts(addr, isObj));
		if (port) parts.push(renderWithIcon('mdi:pound', port, 'text-zinc-400'));
		return `<span class="inline-flex items-center gap-1.5">${parts.join('<span class="text-muted-foreground/40 text-xs">·</span>')}</span>`;
	}

	async function refreshCounters(intent: 'auto' | 'manual' = 'auto') {
		if (countersUpdating) {
			return;
		}

		countersUpdating = true;
		counterFetchIntent = intent;
		try {
			await countersResource.refetch();
		} finally {
			counterFetchIntent = 'auto';
			countersUpdating = false;
		}
	}

	async function handleRowMoved(rows: Row[]) {
		const visibleRows = rows.filter((row) => row.visible !== false);
		const payload: FirewallReorderRequest[] = visibleRows.map((row, index) => ({
			id: Number(row.id),
			priority: index + 1
		}));
		if (payload.length === 0) {
			await trafficRulesResource.refetch();
			return;
		}
		const result = await reorderFirewallTrafficRules(payload);
		if (result.status === 'success') {
			await trafficRulesResource.refetch();
		} else {
			handleAPIError(result);
			toast.error('Failed to reorder traffic rules', { position: 'bottom-center' });
			await trafficRulesResource.refetch();
		}
	}

	async function confirmDelete() {
		if (!activeRow || activeRow.length !== 1) return;
		const id = Number(activeRow[0]?.id);
		const result = await deleteFirewallTrafficRule(id);

		if (result.status === 'success') {
			toast.success('Traffic rule deleted', { position: 'bottom-center' });
			await trafficRulesResource.refetch();
			activeRow = null;
			modals.delete.open = false;
		} else {
			handleAPIError(result);
			toast.error('Failed to delete traffic rule', { position: 'bottom-center' });
		}
	}

	let columns: Column[] = $derived([
		{ field: 'id', title: 'ID', visible: false },
		{ field: 'direction', title: 'direction', visible: false },
		{ field: 'protocol', title: 'protocol', visible: false },
		{ field: 'family', title: 'family', visible: false },
		{ field: 'srcPort', title: 'srcPort', visible: false },
		{ field: 'dstPort', title: 'dstPort', visible: false },
		{ field: 'sourceIsObj', title: 'sourceIsObj', visible: false },
		{ field: 'destIsObj', title: 'destIsObj', visible: false },
		{
			field: 'enabled',
			width: '5%',
			title: '',
			formatter: (cell: CellComponent) => formatEnabled(cell.getValue())
		},
		{ field: 'priority', title: 'Index', width: '5%', sorter: 'number' },
		{
			field: 'hits',
			title: 'Hits',
			formatter: (cell: CellComponent) => Number(cell.getValue() ?? 0).toLocaleString()
		},
		{
			field: 'bytes',
			title: 'Bytes',
			formatter: (cell: CellComponent) => formatBytesBinary(cell.getValue(), { fallback: '0 B' })
		},
		{
			field: 'action',
			title: 'Action',
			formatter: (cell: CellComponent) => {
				const d = cell.getRow().getData();
				return formatAction(d.action, d.direction, d.quick, d.log);
			}
		},
		{
			field: 'name',
			title: 'Name',
			formatter: (cell: CellComponent) => {
				const name = String(cell.getValue() ?? '');
				const d = cell.getRow().getData();
				if (d.visible === false) {
					return `<span class="inline-flex items-center gap-1.5">${name} <span class="inline-flex items-center text-xs font-mono px-1 rounded border text-zinc-400 border-zinc-400/50 leading-tight">MANAGED</span></span>`;
				}
				return name;
			}
		},
		{
			field: 'ingressInterfaces',
			title: 'Ingress',
			formatter: (cell: CellComponent) => {
				const v = cell.getValue();
				if (!v || v === 'any') return renderWithIcon('mdi:earth', 'Any', 'text-sky-400');
				return renderWithIcon('mdi:arrow-down-circle-outline', v, 'text-blue-400');
			}
		},
		{
			field: 'egressInterfaces',
			title: 'Egress',
			formatter: (cell: CellComponent) => {
				const v = cell.getValue();
				if (!v || v === 'any') return renderWithIcon('mdi:earth', 'Any', 'text-sky-400');
				return renderWithIcon('mdi:arrow-up-circle-outline', v, 'text-orange-400');
			}
		},
		{
			field: 'source',
			title: 'Source',
			formatter: (cell: CellComponent) => {
				const d = cell.getRow().getData();
				return formatSource(cell.getValue(), d.sourceIsObj, d.family, d.srcPort);
			}
		},
		{
			field: 'destination',
			title: 'Destination',
			formatter: (cell: CellComponent) => {
				const d = cell.getRow().getData();
				return formatDestination(cell.getValue(), d.destIsObj, d.protocol, d.dstPort);
			}
		},
		{
			field: 'updatedAt',
			title: 'Updated',
			formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
		}
	]);

	let tableData = $derived({
		columns,
		rows: trafficRules.map((rule) => {
			const counter = counterByRuleID.get(rule.id);
			return {
				id: rule.id,
				name: rule.name,
				action: rule.action,
				log: rule.log ?? false,
				quick: rule.quick ?? false,
				direction: rule.direction,
				protocol: rule.protocol,
				family: rule.family ?? 'any',
				priority: rule.priority,
				ingressInterfaces:
					(rule.ingressInterfaces ?? []).map(resolveInterfaceName).join(', ') || 'any',
				egressInterfaces:
					(rule.egressInterfaces ?? []).map(resolveInterfaceName).join(', ') || 'any',
				source: rule.sourceRaw || formatObjectName(rule.sourceObjId),
				sourceIsObj: !rule.sourceRaw && !!rule.sourceObjId,
				srcPort: resolvePortValue(rule.srcPortsRaw, rule.srcPortObjId),
				destination: rule.destRaw || formatObjectName(rule.destObjId),
				destIsObj: !rule.destRaw && !!rule.destObjId,
				dstPort: resolvePortValue(rule.dstPortsRaw, rule.dstPortObjId),
				enabled: rule.enabled ?? true,
				visible: rule.visible ?? true,
				hits: counter?.packets ?? 0,
				bytes: counter?.bytes ?? 0,
				updatedAt: rule.updatedAt
			};
		})
	});

	onMount(() => {
		void refreshCounters('auto');

		const refreshInterval = setInterval(() => {
			if (document.visibilityState === 'visible') {
				void refreshCounters('auto');
			}
		}, 5000);

		const onVisibilityChange = () => {
			if (document.visibilityState === 'visible') {
				void refreshCounters('auto');
			}
		};
		document.addEventListener('visibilitychange', onVisibilityChange);

		return () => {
			clearInterval(refreshInterval);
			document.removeEventListener('visibilitychange', onVisibilityChange);
		};
	});
</script>

{#snippet button(type: string)}
	{#if activeRow !== null && activeRow.length === 1}
		{#if type === 'edit-rule' && activeRow[0]?.visible !== false}
			<Button
				onclick={() => {
					modals.edit.open = true;
					modals.edit.id = Number(activeRow![0]?.id);
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
		{/if}

		{#if type === 'delete-rule' && activeRow[0]?.visible !== false}
			<Button onclick={() => (modals.delete.open = true)} size="sm" variant="outline" class="h-6.5">
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button onclick={() => (modals.create.open = true)} size="sm" class="h-6">
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{@render button('edit-rule')}
		{@render button('delete-rule')}

		<Button
			onclick={() => refreshCounters('manual')}
			size="sm"
			variant="outline"
			class="ml-auto h-6"
			title="Refresh Counters"
		>
			<span class="icon-[mdi--refresh] h-4 w-4"></span>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="tt-firewall-traffic"
			bind:parentActiveRow={activeRow}
			bind:query
			initialSort={[{ column: 'priority', dir: 'asc' }]}
			persistSort={false}
			movable={true}
			onRowMoved={handleRowMoved}
			rowFormatter={(row: RowComponent) => {
				const d = row.getData();
				if (d.visible === false) {
					row.getElement().classList.add('managed-row');
				}
			}}
		/>
	</div>
</div>

{#if modals.create.open}
	<Form
		bind:open={modals.create.open}
		{trafficRules}
		{objects}
		{interfaces}
		{switches}
		{wgClients}
		edit={false}
		afterChange={() => trafficRulesResource.refetch()}
	/>
{/if}

{#if modals.edit.open}
	<Form
		bind:open={modals.edit.open}
		{trafficRules}
		{objects}
		{interfaces}
		{switches}
		{wgClients}
		edit={true}
		id={modals.edit.id}
		afterChange={() => trafficRulesResource.refetch()}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{
		parent: 'traffic rule',
		element: activeRow && activeRow.length === 1 ? String(activeRow[0]?.name ?? '') : ''
	}}
	actions={{
		onConfirm: async () => {
			await confirmDelete();
		},
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>

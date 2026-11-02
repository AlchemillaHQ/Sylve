<script lang="ts">
	import { deleteContainerNetwork, getContainerNetworks } from '$lib/api/container/resources';
	import NetworkForm from '$lib/components/custom/Container/NetworkForm.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { ContainerNetwork } from '$lib/types/container/container';
	import { emptySwitchList, isSwitchList } from '$lib/types/network/switch';
	import { toastContainerError } from '$lib/utils/container/errors';
	import { updateCache } from '$lib/utils/http';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	// svelte-ignore state_referenced_locally
	const networks = resource(
		() => data.node,
		async (node) => {
			const result = await getContainerNetworks(node);
			updateCache('container-networks', result, node);
			return result;
		},
		{ initialValue: data.networks }
	);

	let switches = $derived(isSwitchList(data.switches) ? data.switches : emptySwitchList());
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? networks.current.find((network) => network.id === activeRows?.[0].id)
			: undefined
	);
	let modals = $state({ create: false, delete: false });

	function switchName(network: ContainerNetwork): string {
		if (network.switchId === null) return 'NAT';
		const list = network.switchType === 'manual' ? switches.manual : switches.standard;
		return list.find((sw) => sw.id === network.switchId)?.name ?? network.bridge;
	}

	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{ field: 'name', title: 'Name' },
			{ field: 'switch', title: 'Switch' },
			{ field: 'bridge', title: 'Bridge' },
			{ field: 'subnet', title: 'Subnet' },
			{ field: 'gateway', title: 'Gateway' },
			{ field: 'range', title: 'Range' }
		],
		rows: networks.current.map((network) => ({
			id: network.id,
			name: network.name,
			switch: switchName(network),
			bridge: network.bridge,
			subnet: network.subnet,
			gateway: network.gateway,
			range: [network.rangeStart, network.rangeEnd].filter(Boolean).join(' - ') || '-'
		}))
	});

	async function remove() {
		if (!selected) return;
		const name = selected.name;
		const result = await deleteContainerNetwork(selected.id, data.node);
		if (result.status === 'error') {
			toastContainerError(result);
			return;
		}

		toast.success(`Network ${name} deleted`, { position: 'bottom-center' });
		activeRows = null;
		await networks.refetch();
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create = true)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if selected}
			<Button size="sm" variant="outline" class="h-6.5" onclick={() => (modals.delete = true)}>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	</div>

	<TreeTable
		data={table}
		name="tt-container-networks"
		multipleSelect={false}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.create}
	<NetworkForm
		bind:open={modals.create}
		node={data.node}
		{switches}
		afterChange={() => networks.refetch()}
	/>
{/if}

<AlertDialog
	bind:open={modals.delete}
	names={{ parent: 'container network', element: selected?.name ?? '' }}
	actions={{ onConfirm: remove, onCancel: () => (modals.delete = false) }}
/>

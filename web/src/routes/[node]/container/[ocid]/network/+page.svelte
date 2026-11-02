<script lang="ts">
	import EditDialog from '$lib/components/custom/Container/EditDialog.svelte';
	import Spec from '$lib/components/custom/Container/Spec.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import {
		containerBusyTitle,
		containerSpecOf,
		type ContainerContext
	} from '$lib/types/container/container';
	import { formatPort } from '$lib/utils/container/ports';
	import { getContext } from 'svelte';

	let { data }: { data: { node: string } } = $props();

	const container = getContext<ContainerContext>('container');
	let current = $derived(container.current);
	let inherited = $derived(!current.network);

	const columns: Column[] = [
		{ title: 'Network', field: 'network' },
		{ title: 'Type', field: 'type' },
		{ title: 'IPv4', field: 'ipv4' },
		{ title: 'MAC', field: 'mac' },
		{ title: 'Hostname', field: 'hostname' },
		{ title: 'Published Ports', field: 'ports' }
	];
	let rows = $derived.by((): Row[] => {
		const network = current.network;
		if (!network) return [];
		return [
			{
				id: network.id,
				network: `${network.name} (${network.subnet})`,
				type: network.switchId ? `Switch (${network.bridge})` : 'NAT',
				ipv4: current.runtime?.ips.join(', ') || current.ip || 'Automatic',
				mac: current.runtime?.macs.join(', ') || '-',
				hostname: current.hostname || current.name,
				ports: current.ports.map(formatPort).join(', ') || '-'
			}
		];
	});

	let activeRows = $state<Row[]>([]);
	let query = $state('');
	let inheriting = $state(false);
	let dialog = $state({ open: false, spec: containerSpecOf(container.current) });

	function edit() {
		const spec = containerSpecOf(current);
		if (inherited) spec.network = 'default';
		dialog = { open: true, spec };
	}

	async function inherit() {
		const spec = { ...containerSpecOf(current), network: 'host', ip: '', hostname: '', ports: [] };
		if (await container.update(spec)) {
			inheriting = false;
			activeRows = [];
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border p-2">
		<Button
			onclick={() => (inherited ? edit() : (inheriting = true))}
			size="sm"
			variant="outline"
			disabled={container.busy}
			title={container.busy ? containerBusyTitle : ''}
			class="h-6.5 disabled:pointer-events-auto! {activeRows.length > 0 ? 'hidden' : ''}"
		>
			<SpanWithIcon
				icon={inherited ? 'icon-[mdi--close-network]' : 'icon-[mdi--plus-network]'}
				size="h-4 w-4"
				gap="gap-1"
				title={inherited ? 'Stop Inheriting Network' : 'Inherit Network'}
			/>
		</Button>

		{#if activeRows.length === 1}
			<Button
				size="sm"
				class="h-6 disabled:pointer-events-auto!"
				variant="outline"
				disabled={container.busy}
				title={container.busy ? containerBusyTitle : ''}
				onclick={edit}
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit" />
			</Button>
		{/if}
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={{ columns, rows }}
			name="container-network-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if dialog.open}
	<EditDialog
		bind:open={dialog.open}
		title="Network"
		icon="icon-[mdi--lan]"
		width="sm:max-w-3xl"
		onSave={() => container.update(dialog.spec)}
	>
		<div class="flex flex-col gap-4">
			<Spec bind:spec={dialog.spec} section="network" node={data.node} pool={current.pool} />
			<Spec
				bind:spec={dialog.spec}
				section="ports"
				node={data.node}
				pool={current.pool}
				ocId={current.ocId}
			/>
		</div>
	</EditDialog>
{/if}

<AlertDialog
	bind:open={inheriting}
	customTitle="This will make the container share the host's network stack. Its address, hostname and published ports are removed."
	keepOpenOnConfirm={true}
	actions={{ onConfirm: inherit, onCancel: () => (inheriting = false) }}
/>

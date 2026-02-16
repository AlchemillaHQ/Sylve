<script lang="ts">
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { storage } from '$lib';
	import type { ClusterNode } from '$lib/types/cluster/cluster';
	import { watch } from 'runed';

	interface Props {
		name: string;
		hostname: string;
		id: number;
		description: string;
		refetch: boolean;
		nodes: ClusterNode[];
		node: string;
	}

	let {
		name = $bindable(),
		id = $bindable(),
		hostname = $bindable(),
		description = $bindable(),
		refetch = $bindable(),
		nodes,
		node = $bindable()
	}: Props = $props();

	let host = $state({
		combobox: {
			open: false
		}
	});

	let hosts = $derived.by(() => {
		return nodes.map((n) => ({
			label: n.hostname,
			value: n.hostname
		}));
	});

	watch(
		() => node,
		() => {
			storage.hostname = node;
			refetch = true;
		}
	);
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 {hosts.length > 0 ? 'md:grid-cols-4' : 'md:grid-cols-3'}">
		{#if hosts.length > 0}
			<CustomComboBox
				bind:open={host.combobox.open}
				label="Node"
				bind:value={node}
				data={hosts}
				classes="flex-1 space-y-1"
				placeholder="Select Node"
				triggerWidth="w-full "
				width="w-full lg:w-[75%]"
			></CustomComboBox>
		{/if}

		<CustomValueInput
			label="Jail Name"
			placeholder="Postgres"
			bind:value={name}
			classes="flex-1 space-y-1"
		/>

		<CustomValueInput
			label="Hostname"
			placeholder="postgres"
			bind:value={hostname}
			classes="flex-1 space-y-1"
		/>

		<CustomValueInput
			label="Jail ID"
			placeholder="100"
			type="number"
			bind:value={id}
			classes="flex-1 space-y-1"
		/>
	</div>

	<CustomValueInput
		label="Description"
		placeholder="Optional description for this virtual machine"
		type="textarea"
		textAreaClasses="min-h-40"
		bind:value={description}
		classes="flex-1 space-y-1"
	/>
</div>

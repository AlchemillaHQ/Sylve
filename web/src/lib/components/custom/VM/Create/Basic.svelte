<script lang="ts">
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { storage } from '$lib';
	import type { ClusterNode } from '$lib/types/cluster/cluster';

	interface Props {
		name: string;
		node: string;
		id: number;
		description: string;
		nodes: ClusterNode[];
		refetch: boolean;
	}

	let {
		name = $bindable(),
		id = $bindable(),
		description = $bindable(),
		node = $bindable(),
		nodes,
		refetch = $bindable()
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

	$effect(() => {
		if (node) {
			storage.hostname = node;
			refetch = true;
		}
	});
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
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
			label="VM Name"
			placeholder="my-virtual-machine"
			bind:value={name}
			classes="flex-1 space-y-1"
		/>

		<CustomValueInput
			label="VM ID"
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

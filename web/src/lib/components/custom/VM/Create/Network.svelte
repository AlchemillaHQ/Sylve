<script lang="ts">
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import { Label } from '$lib/components/ui/label/index.js';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { onMount } from 'svelte';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { VM } from '$lib/types/vm/vm';
	import { generateMACOptions } from '$lib/utils/network/object';

	interface Props {
		switch: string;
		mac: string;
		emulation: string;
		switches: SwitchList;
		vms: VM[];
		networkObjects: NetworkObject[];
	}

	let {
		switch: nwSwitch = $bindable(),
		mac = $bindable(),
		emulation = $bindable(),
		switches,
		networkObjects,
		vms
	}: Props = $props();

	let usableMacs = $derived.by(() => {
		const usedMacIds = new Set<number>(
			vms
				.flatMap((vm) => vm.networks.map((net) => net.macId))
				.filter((id): id is number => id !== undefined)
		);

		return networkObjects.filter(
			(obj) => obj.type === 'Mac' && obj.entries?.length === 1 && !usedMacIds.has(obj.id)
		);
	});

	let comboBoxes = $state({
		emulation: {
			open: false,
			value: 'virtio',
			options: [
				{ label: 'VirtIO', value: 'virtio' },
				{ label: 'E1000', value: 'e1000' }
			]
		},
		mac: {
			open: false,
			value: '0'
		}
	});

	$effect(() => {
		if (comboBoxes.mac.value) {
			mac = comboBoxes.mac.value;
		}
	});
</script>

{#snippet radioItem(id: number, name: string, type: 'standard' | 'manual' | 'none')}
	{@const i = `radio-${id}`}
	<div class="mb-2 flex items-center space-x-3 rounded-lg border p-4">
		<RadioGroup.Item value={name} id={i} />
		<Label for={i} class="flex flex-col items-start gap-2">
			<p class="">{name}</p>
			<p class="text-muted-foreground text-sm">
				{#if type === 'none'}
					No network switch will be allocated now, you can add it later
				{:else if type === 'manual'}
					Manual switch
				{:else}
					Standard switch
				{/if}
			</p>
		</Label>
	</div>
{/snippet}

<div class="flex flex-col gap-4 p-4">
	<RadioGroup.Root bind:value={nwSwitch} class="border p-2">
		<ScrollArea orientation="vertical" class="h-64 w-full max-w-full">
			{#if switches && switches.standard}
				{#each switches.standard ?? [] as sw}
					{@render radioItem(sw.id, sw.name, 'standard')}
				{/each}
			{/if}

			{#if switches && switches.manual}
				{#each switches.manual ?? [] as sw}
					{@render radioItem(sw.id, sw.name, 'manual')}
				{/each}
			{/if}

			{@render radioItem(0, 'None', 'none')}
		</ScrollArea>
	</RadioGroup.Root>

	{#if nwSwitch !== 'None' && nwSwitch !== ''}
		<div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
			<CustomComboBox
				bind:open={comboBoxes.emulation.open}
				label="Emulation Type"
				bind:value={emulation}
				data={comboBoxes.emulation.options}
				classes="flex-1 space-y-1"
				placeholder="Emulation type"
				width="w-3/4"
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.mac.open}
				label="MAC Address"
				bind:value={comboBoxes.mac.value}
				data={generateMACOptions(usableMacs)}
				classes="flex-1 space-y-1"
				placeholder="Select MAC address"
				width="w-full"
			></CustomComboBox>
		</div>
	{/if}
</div>

<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { SwitchList } from '$lib/types/network/switch';
	import { Label } from '$lib/components/ui/label/index.js';
	import type { NetworkObject } from '$lib/types/network/object';
	import { generateMACOptions } from '$lib/utils/network/object';

	interface Props {
		switch: string;
		mac: string;
		emulation: string;
		switches: SwitchList;
		networkObjects: NetworkObject[];
	}

	let {
		switch: nwSwitch = $bindable(),
		mac = $bindable(),
		emulation = $bindable(),
		switches,
		networkObjects
	}: Props = $props();

	let usableMacs = $derived.by(() => {
		return networkObjects.filter(
			(obj) => obj.type === 'Mac' && obj.entries?.length === 1 && obj.isUsed === false
		);
	});
	// @wc-ignore
	const emulationOptions = [
		{ label: 'VirtIO', value: 'virtio' },
		{ label: 'E1000', value: 'e1000' }
	];

	let comboBoxes = $state({
		emulation: {
			open: false,
			value: 'virtio'
		},
		mac: {
			open: false
		}
	});
</script>

{#snippet radioItem(
	id: number,
	name: string,
	type: 'standard' | 'manual' | 'none',
	vlanFiltering = false,
	defaultAccessVlan: number | null = null,
	runtimeAvailable = true
)}
	{@const i = `radio-${type}-${id}`}
	{@const unavailable = !runtimeAvailable || (vlanFiltering && defaultAccessVlan === null)}
	<div
		class:opacity-60={unavailable}
		class="mb-2 flex items-center space-x-3 rounded-lg border p-4"
	>
		<RadioGroup.Item value={name} id={i} disabled={unavailable} />
		<Label for={i} class="flex flex-col items-start gap-2">
			<p class="">{name}</p>
			<p class="text-muted-foreground text-sm">
				{#if !runtimeAvailable}
					Runtime bridge state unavailable
				{:else if type === 'none'}
					No network switch will be allocated now, you can add it later
				{:else if type === 'manual'}
					Manual switch{vlanFiltering && defaultAccessVlan !== null
						? ` · VLAN filtered · access VLAN ${defaultAccessVlan}`
						: vlanFiltering
							? ' · VLAN filtered · no default access VLAN (unavailable for VMs)'
							: ''}
				{:else}
					Standard switch{vlanFiltering && defaultAccessVlan !== null
						? ` · VLAN filtered · access VLAN ${defaultAccessVlan}`
						: vlanFiltering
							? ' · VLAN filtered · no default access VLAN (unavailable for VMs)'
							: ''}
				{/if}
			</p>
		</Label>
	</div>
{/snippet}

<div class="flex flex-col gap-4 p-4">
	<RadioGroup.Root bind:value={nwSwitch} class="border p-2">
		<ScrollArea orientation="vertical" class="h-64 w-full max-w-full">
			{#if switches && switches.standard}
				{#each switches.standard ?? [] as sw (sw.id)}
					{@render radioItem(sw.id, sw.name, 'standard', sw.vlanFiltering, sw.defaultAccessVlan)}
				{/each}
			{/if}

			{#if switches && switches.manual}
				{#each switches.manual ?? [] as sw (sw.id)}
					{@render radioItem(
						sw.id,
						sw.name,
						'manual',
						sw.vlanFiltering,
						sw.defaultAccessVlan,
						sw.vlanStateAvailable
					)}
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
				data={emulationOptions}
				classes="flex-1 space-y-1"
				placeholder="Emulation type"
				width="w-3/4"
			></CustomComboBox>

			<CustomComboBox
				bind:open={comboBoxes.mac.open}
				label="MAC Address"
				bind:value={mac}
				data={generateMACOptions(usableMacs)}
				classes="flex-1 space-y-1"
				placeholder="Select MAC address"
				width="w-full"
			></CustomComboBox>
		</div>
	{/if}
</div>

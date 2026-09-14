<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import Label from '$lib/components/ui/label/label.svelte';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import type {
		SelectOption,
		StandardSwitchFormComboBoxes,
		StandardSwitchFormState
	} from './types';
	import VLANPolicyEditor from '$lib/components/custom/Network/VLANPolicyEditor.svelte';

	interface Props {
		form: StandardSwitchFormState;
		comboBoxes: StandardSwitchFormComboBoxes;
		portOptions: SelectOption[];
		bridgeMACPortOptions: SelectOption[];
		bridgeMACObjectOptions: SelectOption[];
		effectiveBridgeMAC: string;
		error?: string;
		onClearError: () => void;
		onCreateMACObject: () => void;
	}

	let {
		form = $bindable(),
		comboBoxes = $bindable(),
		portOptions,
		bridgeMACPortOptions,
		bridgeMACObjectOptions,
		effectiveBridgeMAC,
		error = '',
		onClearError,
		onCreateMACObject
	}: Props = $props();
	let selectedPorts = $derived([...comboBoxes.ports.value].sort());
</script>

<div class="space-y-5 pb-1">
	{#if error}
		<div
			class="border-destructive/40 bg-destructive/10 text-destructive rounded-md border px-3 py-2 text-sm"
			role="alert"
		>
			{error}
		</div>
	{/if}

	<div class="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-3">
		{#if form.vlanFiltering}
			<CustomValueInput
				label="Default access VLAN"
				placeholder="Optional"
				bind:value={form.defaultAccessVlan}
				type="number"
				hint="New unconfigured members, including VM TAPs, inherit this untagged VLAN. Leave empty to reject VM attachments."
				onChange={onClearError}
			/>
			<CustomValueInput
				label="Host VLAN"
				placeholder="Optional"
				bind:value={form.hostVlan}
				type="number"
				hint="Required for host IPv4/IPv6 on a filtered switch. Sylve creates a VLAN interface on the bridge; its VLAN must be admitted by the uplink."
				onChange={onClearError}
			/>
		{:else}
			<CustomValueInput
				label="VLAN child"
				placeholder="0"
				bind:value={form.vlan}
				type="number"
				hint="Create VLAN child interfaces before adding the selected ports. Use 0 for none."
				onChange={onClearError}
			/>
		{/if}

		<CustomComboBox
			bind:open={comboBoxes.ports.open}
			label="Ports"
			bind:value={comboBoxes.ports.value}
			data={portOptions}
			classes={form.vlanFiltering ? 'space-y-1.5' : 'space-y-1.5 md:col-span-2'}
			placeholder="Select ports"
			multiple={true}
			width="w-full"
		/>
	</div>

	{#if form.vlanFiltering && selectedPorts.length > 0}
		<div class="space-y-3">
			<div>
				<p class="text-sm font-semibold">Physical port policies</p>
				<p class="text-muted-foreground mt-1 text-xs">
					Port selection order has no effect. Every selected port needs an explicit policy.
				</p>
			</div>
			{#each selectedPorts as port (port)}
				{#if form.portPolicies[port]}
					<VLANPolicyEditor
						title={port}
						description="Ingress and egress VLAN handling"
						idPrefix={`policy-${port}`}
						bind:mode={form.portPolicies[port].mode}
						bind:untagged={form.portPolicies[port].untaggedVlan}
						bind:tagged={form.portPolicies[port].taggedVlans}
						onChange={onClearError}
					/>
				{/if}
			{/each}
		</div>
	{/if}

	<div class="rounded-md border p-4">
		<CustomCheckbox
			label="VLAN filtering"
			bind:checked={form.vlanFiltering}
			classes="flex items-center gap-2"
		/>
		<p class="text-muted-foreground mt-2 text-xs">
			Choose the untagged and tagged VLANs on each port
		</p>
	</div>

	<div class="space-y-2 rounded-md border p-3">
		<Label>Bridge MAC source</Label>
		<RadioGroup.Root bind:value={form.bridgeMacMode} class="grid gap-2 sm:grid-cols-2">
			<label
				for="bridge-mac-source-port"
				class="flex cursor-pointer items-start gap-3 rounded-md border p-3"
			>
				<RadioGroup.Item id="bridge-mac-source-port" value="port" class="mt-1" />
				<div>
					<p class="text-sm font-medium">Use port MAC</p>
					<p class="text-muted-foreground text-xs">
						Keep the bridge identity tied to one selected port.
					</p>
				</div>
			</label>
			<label
				for="bridge-mac-source-object"
				class="flex cursor-pointer items-start gap-3 rounded-md border p-3"
			>
				<RadioGroup.Item id="bridge-mac-source-object" value="object" class="mt-1" />
				<div>
					<p class="text-sm font-medium">Use MAC object</p>
					<p class="text-muted-foreground text-xs">
						Use an explicit MAC, including on a portless switch.
					</p>
				</div>
			</label>
		</RadioGroup.Root>

		{#if form.bridgeMacMode === 'port'}
			<CustomComboBox
				bind:open={comboBoxes.bridgeMacPort.open}
				label="MAC source port"
				bind:value={comboBoxes.bridgeMacPort.value}
				data={bridgeMACPortOptions}
				placeholder="Select one of the switch ports"
				width="w-full"
				disallowEmpty={true}
			/>
		{:else if form.bridgeMacMode === 'object'}
			<div class="flex items-end gap-2">
				<CustomComboBox
					bind:open={comboBoxes.bridgeMacObject.open}
					label="MAC address object"
					bind:value={comboBoxes.bridgeMacObject.value}
					data={bridgeMACObjectOptions}
					placeholder="Select a single-value MAC object"
					classes="min-w-0 flex-1 space-y-1"
					width="w-full"
					disallowEmpty={true}
				/>
				<Button variant="outline" size="sm" class="h-9" onclick={onCreateMACObject}>
					<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="Create object" />
				</Button>
			</div>
		{/if}

		{#if effectiveBridgeMAC}
			<p class="text-muted-foreground text-xs">
				Effective MAC: <span class="font-mono">{effectiveBridgeMAC}</span>
			</p>
		{/if}
	</div>
</div>

<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import type {
		SelectOption,
		StandardSwitchFormComboBoxes,
		StandardSwitchFormState
	} from './types';

	interface Props {
		version: 4 | 6;
		form: StandardSwitchFormState;
		comboBoxes: StandardSwitchFormComboBoxes;
		networkOptions: SelectOption[];
		gatewayOptions: SelectOption[];
		error?: string;
	}

	let {
		version,
		form = $bindable(),
		comboBoxes = $bindable(),
		networkOptions,
		gatewayOptions,
		error = ''
	}: Props = $props();
	let filteredWithoutHostVLAN = $derived(
		form.vlanFiltering && String(form.hostVlan ?? '').trim() === ''
	);
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

	{#if filteredWithoutHostVLAN}
		<div class="bg-muted/40 rounded-md border p-4 text-sm">
			<p class="font-medium">Host IPv{version} is disabled until a Host VLAN is selected.</p>
			<p class="text-muted-foreground mt-1 text-xs">
				Set the Host VLAN under Ports & VLANs and Sylve will create the required VLAN interface on
				top of the filtered bridge.
			</p>
		</div>
	{:else if version === 4}
		<div class="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2">
			<CustomComboBox
				bind:open={comboBoxes.ipv4.open}
				label="IPv4 Network"
				bind:value={comboBoxes.ipv4.value}
				data={networkOptions}
				classes="flex-1 space-y-1"
				placeholder="Select object or type CIDR (192.168.1.1/24)"
				width="w-full"
				disabled={form.dhcp}
				multiple={false}
				allowCustom={true}
			/>

			<CustomComboBox
				bind:open={comboBoxes.ipv4Gw.open}
				label="IPv4 Gateway"
				bind:value={comboBoxes.ipv4Gw.value}
				data={gatewayOptions}
				classes="flex-1 space-y-1"
				placeholder="Select object or type IP (192.168.1.254)"
				width="w-full"
				disabled={form.dhcp}
				multiple={false}
				allowCustom={true}
			/>
		</div>

		<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
			<div class="rounded-md border p-4">
				<CustomCheckbox label="DHCP" bind:checked={form.dhcp} classes="flex items-center gap-2" />
				<p class="text-muted-foreground mt-2 whitespace-nowrap text-xs">
					Get an IPv4 address automatically
				</p>
			</div>
			<div class="rounded-md border p-4">
				<CustomCheckbox
					label={form.dhcp ? 'Use DHCP Default Route' : 'IPv4 Default Route'}
					bind:checked={form.defaultRoute}
					classes="flex items-center gap-2"
				/>
				<p class="text-muted-foreground mt-2 whitespace-nowrap text-xs">
					{form.dhcp ? 'Use the gateway provided by DHCP' : 'Use the configured gateway'}
				</p>
			</div>
		</div>
	{:else}
		<div class="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2">
			<CustomComboBox
				bind:open={comboBoxes.ipv6.open}
				label="IPv6 Network"
				bind:value={comboBoxes.ipv6.value}
				data={networkOptions}
				classes="flex-1 space-y-1"
				placeholder="Select object or type CIDR (2001:db8::1/64)"
				width="w-full"
				disabled={form.disableIPv6 || form.slaac}
				multiple={false}
				allowCustom={true}
			/>

			<CustomComboBox
				bind:open={comboBoxes.ipv6Gw.open}
				label="IPv6 Gateway"
				bind:value={comboBoxes.ipv6Gw.value}
				data={gatewayOptions}
				classes="flex-1 space-y-1"
				placeholder="Select object or type IP (2001:db8::1)"
				width="w-full"
				disabled={form.disableIPv6 || form.slaac}
				multiple={false}
				allowCustom={true}
			/>
		</div>

		<div class="grid grid-cols-1 gap-3 md:grid-cols-3">
			<div class="rounded-md border p-4">
				<CustomCheckbox
					label="Disable IPv6"
					bind:checked={form.disableIPv6}
					classes="flex items-center gap-2"
				/>
				<p class="text-muted-foreground mt-2 whitespace-nowrap text-xs">
					No SLAAC or static addresses
				</p>
			</div>
			<div class="rounded-md border p-4">
				<CustomCheckbox label="SLAAC" bind:checked={form.slaac} classes="flex items-center gap-2" />
				<p class="text-muted-foreground mt-2 whitespace-nowrap text-xs">
					Get IPv6 addresses from RAs
				</p>
			</div>
			<div class="rounded-md border p-4">
				<CustomCheckbox
					label={form.slaac ? 'Use RA Default Route' : 'IPv6 Default Route'}
					bind:checked={form.defaultRoute6}
					classes="flex items-center gap-2"
					disabled={form.disableIPv6}
				/>
				<p class="text-muted-foreground mt-2 whitespace-nowrap text-xs">
					{form.slaac ? 'Use the advertised router' : 'Use the configured gateway'}
				</p>
			</div>
		</div>
	{/if}
</div>

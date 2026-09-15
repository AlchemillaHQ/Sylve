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
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import IPTab from './IPTab.svelte';
	import PortsTab from './PortsTab.svelte';
	import type {
		SelectOption,
		StandardSwitchFormComboBoxes,
		StandardSwitchFormState,
		SwitchTab
	} from './types';

	interface Props {
		mode: 'create' | 'edit';
		form: StandardSwitchFormState;
		comboBoxes: StandardSwitchFormComboBoxes;
		activeTab: SwitchTab;
		portOptions: SelectOption[];
		ipv4NetworkOptions: SelectOption[];
		ipv4GatewayOptions: SelectOption[];
		ipv6NetworkOptions: SelectOption[];
		ipv6GatewayOptions: SelectOption[];
		bridgeMACPortOptions: SelectOption[];
		bridgeMACObjectOptions: SelectOption[];
		effectiveBridgeMAC: string;
		saving: boolean;
		onReset: () => void;
		onClose: () => void;
		onSubmit: () => void;
		onCreateMACObject: () => void;
	}

	let {
		mode,
		form = $bindable(),
		comboBoxes = $bindable(),
		activeTab = $bindable(),
		portOptions,
		ipv4NetworkOptions,
		ipv4GatewayOptions,
		ipv6NetworkOptions,
		ipv6GatewayOptions,
		bridgeMACPortOptions,
		bridgeMACObjectOptions,
		effectiveBridgeMAC,
		saving,
		onReset,
		onClose,
		onSubmit,
		onCreateMACObject
	}: Props = $props();

	function preventWhileSaving(event: Event) {
		if (saving) event.preventDefault();
	}
</script>

<Dialog.Root
	bind:open={form.open}
	onOpenChangeComplete={(open) => {
		if (!open) onClose();
	}}
>
	<Dialog.Content
		class="flex h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] flex-col gap-0 overflow-hidden p-5 sm:h-[82dvh] sm:max-h-[42rem] sm:w-[calc(100vw-4rem)] sm:max-w-4xl! sm:p-6"
		showCloseButton={!saving}
		showResetButton={!saving}
		{onReset}
		{onClose}
		onInteractOutside={(event) => event.preventDefault()}
		onEscapeKeydown={preventWhileSaving}
	>
		<Dialog.Header class="shrink-0 pr-10">
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[clarity--network-switch-line]"
					size="h-6 w-6"
					gap="gap-2"
					title={mode === 'edit'
						? `Edit Standard Switch - ${form.oldName ?? form.name}`
						: 'Create Standard Switch'}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<Tabs.Root bind:value={activeTab} class="mt-4 flex min-h-0 flex-1 flex-col gap-0">
			<Tabs.List class="grid w-full shrink-0 grid-cols-4 p-0">
				<Tabs.Trigger value="general" class="border-b px-1 text-xs sm:text-sm">
					<span class="icon-[mdi--tune-variant] h-4 w-4"></span>
					General
				</Tabs.Trigger>
				<Tabs.Trigger value="ports" class="border-b px-1 text-xs sm:text-sm">
					<span class="icon-[mdi--ethernet] h-4 w-4"></span>
					<span class="hidden sm:inline">Ports & VLANs</span>
					<span class="sm:hidden">Ports</span>
				</Tabs.Trigger>
				<Tabs.Trigger value="ipv4" class="border-b px-1 text-xs sm:text-sm">
					<span class="icon-[mdi--numeric-4-box-outline] h-4 w-4"></span>
					IPv4
				</Tabs.Trigger>
				<Tabs.Trigger value="ipv6" class="border-b px-1 text-xs sm:text-sm">
					<span class="icon-[mdi--numeric-6-box-outline] h-4 w-4"></span>
					IPv6
				</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content
				value="general"
				class="m-0 min-h-0 flex-1 overflow-y-auto overscroll-contain py-2 pr-3"
			>
				<div class="space-y-5 pb-1">
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
						<CustomValueInput
							label="Name"
							placeholder="public"
							bind:value={form.name}
							classes="space-y-1.5"
							disabled={mode === 'edit'}
						/>

						<CustomValueInput
							label="MTU"
							placeholder="1500"
							bind:value={form.mtu}
							classes="space-y-1.5"
							type="number"
							preserveEmpty={true}
						/>
					</div>

					<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
						<div class="rounded-md border p-4">
							<CustomCheckbox
								label="Private"
								bind:checked={form.private}
								classes="flex items-center gap-2"
							/>
							<p class="text-muted-foreground mt-2 text-xs">
								Isolate VMs and jails from each other
							</p>
						</div>
						<div class="rounded-md border p-4">
							<CustomCheckbox
								label="Disable Bridge Offloads"
								bind:checked={form.disableBridgeOffloads}
								classes="flex items-center gap-2"
							/>
							<p class="text-muted-foreground mt-2 text-xs">
								Prevents flapping on member interfaces
							</p>
						</div>
					</div>
				</div>
			</Tabs.Content>

			<Tabs.Content
				value="ports"
				class="m-0 min-h-0 flex-1 overflow-y-auto overscroll-contain py-2 pr-3"
			>
				<PortsTab
					bind:form
					bind:comboBoxes
					{portOptions}
					{bridgeMACPortOptions}
					{bridgeMACObjectOptions}
					{effectiveBridgeMAC}
					{onCreateMACObject}
				/>
			</Tabs.Content>

			<Tabs.Content
				value="ipv4"
				class="m-0 min-h-0 flex-1 overflow-y-auto overscroll-contain py-2 pr-3"
			>
				<IPTab
					version={4}
					bind:form
					bind:comboBoxes
					networkOptions={ipv4NetworkOptions}
					gatewayOptions={ipv4GatewayOptions}
				/>
			</Tabs.Content>

			<Tabs.Content
				value="ipv6"
				class="m-0 min-h-0 flex-1 overflow-y-auto overscroll-contain py-2 pr-3"
			>
				<IPTab
					version={6}
					bind:form
					bind:comboBoxes
					networkOptions={ipv6NetworkOptions}
					gatewayOptions={ipv6GatewayOptions}
				/>
			</Tabs.Content>
		</Tabs.Root>

		<Dialog.Footer class="flex shrink-0 justify-end gap-2 border-t pt-4">
			<Button onclick={onSubmit} type="submit" size="sm" class="w-full sm:w-28" disabled={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-2 h-4 w-4 animate-spin"></span>
					{mode === 'edit' ? 'Saving…' : 'Creating…'}
				{:else}
					{mode === 'edit' ? 'Save' : 'Create'}
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

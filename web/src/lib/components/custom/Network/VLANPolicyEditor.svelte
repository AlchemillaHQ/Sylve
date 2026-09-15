<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as RadioGroup from '$lib/components/ui/radio-group/index.js';
	import { parseTaggedVLANs, type VLANPolicyMode } from '$lib/utils/network/vlan';

	interface Props {
		title: string;
		description?: string;
		idPrefix: string;
		mode: VLANPolicyMode;
		untagged: string | number;
		tagged: string;
	}

	let {
		title,
		description = '',
		idPrefix,
		mode = $bindable(),
		untagged = $bindable(),
		tagged = $bindable()
	}: Props = $props();
	let taggedPreview = $derived(mode === 'trunk' ? parseTaggedVLANs(tagged) : null);
</script>

<div class="space-y-3 rounded-md border p-4">
	<div class="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
		<div>
			<p class="text-sm font-medium">{title}</p>
			{#if description}
				<p class="text-muted-foreground text-xs">{description}</p>
			{/if}
		</div>
		<RadioGroup.Root bind:value={mode} class="flex gap-4" aria-label="VLAN port mode">
			<label class="flex cursor-pointer items-center gap-2 text-sm" for={`${idPrefix}-vlan-access`}>
				<RadioGroup.Item id={`${idPrefix}-vlan-access`} value="access" />
				Access
			</label>
			<label class="flex cursor-pointer items-center gap-2 text-sm" for={`${idPrefix}-vlan-trunk`}>
				<RadioGroup.Item id={`${idPrefix}-vlan-trunk`} value="trunk" />
				Trunk
			</label>
		</RadioGroup.Root>
	</div>

	{#if mode === 'access'}
		<CustomValueInput
			label="Access VLAN"
			placeholder="10"
			bind:value={untagged}
			type="number"
			preserveEmpty={true}
		/>
	{:else if mode === 'trunk'}
		<div class="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2">
			<CustomValueInput
				label="Native VLAN"
				placeholder="Optional"
				bind:value={untagged}
				type="number"
				hint="Untagged traffic is assigned to this VLAN. Leave empty for a tagged-only trunk."
				preserveEmpty={true}
			/>
			<CustomValueInput
				label="Tagged VLANs"
				placeholder="20,30-32"
				bind:value={tagged}
				hint="Comma-separated IDs and inclusive ranges."
			/>
		</div>
		{#if taggedPreview && taggedPreview.length > 0}
			<p class="text-muted-foreground text-xs">Allowed tagged VLANs: {taggedPreview.join(', ')}</p>
		{:else if tagged.trim()}
			<p class="text-destructive text-xs">Enter VLAN IDs or inclusive ranges from 1 to 4094.</p>
		{/if}
	{:else}
		<p class="text-muted-foreground text-xs">Choose how this port carries VLAN traffic.</p>
	{/if}
</div>

<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { mountTypeLabels, type ContainerMount } from '$lib/types/container/container';
	import type { Snippet } from 'svelte';

	interface Props {
		mount: ContainerMount;
		datasets: string[];
		actions?: Snippet;
	}

	let { mount = $bindable(), datasets, actions }: Props = $props();

	const placeholders: Record<ContainerMount['type'], string> = {
		volume: 'Volume name',
		dataset: 'Select a dataset',
		path: '/path/on/host'
	};
</script>

<SimpleSelect
	options={Object.entries(mountTypeLabels).map(([value, label]) => ({ value, label }))}
	bind:value={mount.type}
	onChange={(value) => {
		mount.type = value as ContainerMount['type'];
		mount.source = '';
	}}
	classes={{ parent: 'min-w-0', trigger: 'h-9 w-full' }}
/>
{#if mount.type === 'dataset'}
	<CustomComboBox
		open={false}
		data={datasets.map((name) => ({ value: name, label: name }))}
		bind:value={mount.source}
		placeholder={placeholders.dataset}
		disallowEmpty
		width="w-full"
		classes=""
	/>
{:else}
	<CustomValueInput placeholder={placeholders[mount.type]} bind:value={mount.source} classes="" />
{/if}
<CustomValueInput placeholder="/path/in/container" bind:value={mount.target} classes="" />
<div class="flex gap-2">
	<Button
		variant="outline"
		size="icon"
		class="h-9 w-9 {mount.readOnly ? 'text-yellow-600' : ''}"
		title={mount.readOnly
			? 'Read-only, click to make writable'
			: 'Writable, click to make read-only'}
		onclick={() => (mount.readOnly = !mount.readOnly)}
	>
		<span
			class="{mount.readOnly
				? 'icon-[mdi--lock-outline]'
				: 'icon-[mdi--lock-open-variant-outline]'} h-4 w-4"
		></span>
	</Button>
	{@render actions?.()}
</div>

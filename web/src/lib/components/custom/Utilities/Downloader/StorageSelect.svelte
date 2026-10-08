<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2026 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import type { DownloadStorageChoices } from '$lib/types/utilities/downloader';

	interface Props {
		storageChoices: DownloadStorageChoices;
		value: string;
		disabled?: boolean;
		blocked?: boolean;
	}

	let {
		storageChoices,
		value = $bindable(''),
		disabled = false,
		blocked = $bindable(false)
	}: Props = $props();
	const defaultValue = '__default__';
	let options = $derived(
		storageChoices.choices.map((choice) => {
			const label = choice.storagePool ? choice.label : 'Default';
			return {
				value: choice.storagePool || defaultValue,
				label: choice.available ? label : `${label} — ${choice.reason || 'unavailable'}`,
				disabled: !choice.available
			};
		})
	);

	$effect(() => {
		blocked = !storageChoices.choices.some(
			(choice) => choice.storagePool === value && choice.available
		);
	});
</script>

<div class="space-y-1">
	<SimpleSelect
		label="Storage"
		placeholder="Default"
		{options}
		value={value || defaultValue}
		onChange={(selected) => (value = selected === defaultValue ? '' : selected)}
		{disabled}
		classes={{ parent: 'space-y-1 w-full', label: 'text-sm', trigger: 'w-full' }}
	/>
	{#if storageChoices.error}
		<p class="text-destructive text-xs" role="alert">
			Pool storage discovery failed ({storageChoices.error}). Default is still available.
		</p>
	{/if}
</div>

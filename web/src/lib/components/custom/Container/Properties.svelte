<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';

	interface Props {
		name: string;
		rows: { id: string; property: string; value: string }[];
		disabled: boolean;
		onEdit: (id: string) => void;
	}

	let { name, rows, disabled, onEdit }: Props = $props();

	let activeRows = $state<Row[] | null>(null);
	let active = $derived(activeRows?.length === 1 ? activeRows[0] : null);
	let query = $state('');
	const columns: Column[] = [
		{ title: 'Property', field: 'property' },
		{ title: 'Value', field: 'value' }
	];
</script>

<div class="flex h-full w-full flex-col">
	{#if active}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			<Button
				onclick={() => onEdit(String(active.id))}
				size="sm"
				variant="outline"
				class="h-6.5 disabled:pointer-events-auto!"
				{disabled}
				title={disabled ? 'Wait for the current container operation to finish' : ''}
			>
				<SpanWithIcon
					icon="icon-[mdi--pencil]"
					size="h-4 w-4"
					gap="gap-1"
					title="Edit {active.property}"
				/>
			</Button>
		</div>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={{ columns, rows }}
			{name}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

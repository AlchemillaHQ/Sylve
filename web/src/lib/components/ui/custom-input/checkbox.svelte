<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { generateNanoId } from '$lib/utils/string';

	interface Props {
		id?: string;
		label: string;
		checked: boolean;
		classes?: string;
		disabled?: boolean;
		title?: string;
	}

	let {
		id = '',
		label = '',
		checked = $bindable(false),
		classes = 'flex items-center gap-2',
		disabled = false,
		title = ''
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let nanoId = $state(generateNanoId(label + id));
</script>

{#if title}
	<Tooltip.Root>
		<div class={classes}>
			<Checkbox id={nanoId} bind:checked aria-labelledby={label} {disabled} />
			<Tooltip.Trigger>
				{#snippet child({ props })}
					<Label
						{...props}
						id={nanoId}
						for={nanoId}
						class="text-sm font-medium leading-snug peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
					>
						{label}
					</Label>
				{/snippet}
			</Tooltip.Trigger>
		</div>
		<Tooltip.Content side="top" sideOffset={2}>
			<p>{title}</p>
		</Tooltip.Content>
	</Tooltip.Root>
{:else}
	<div class={classes}>
		<Checkbox id={nanoId} bind:checked aria-labelledby={label} {disabled} />
		<Label
			id={nanoId}
			for={nanoId}
			class="text-sm font-medium leading-snug peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
		>
			{label}
		</Label>
	</div>
{/if}

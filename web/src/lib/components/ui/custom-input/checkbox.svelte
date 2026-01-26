<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox/index.js';
	import { Label } from '$lib/components/ui/label/index.js';
	import { generateNanoId } from '$lib/utils/string';

	interface Props {
		id?: string;
		label: string;
		checked: boolean;
		classes: string;
		disabled?: boolean;
	}

	let {
		id = '',
		label = '',
		checked = $bindable(false),
		classes = 'flex items-center gap-2',
		disabled = false
	}: Props = $props();

	// svelte-ignore state_referenced_locally
	let nanoId = $state(generateNanoId(label + id));
</script>

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

<script lang="ts">
	import { formatDetailValue } from '$lib/stores/error-details.svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		label: string;
		value: unknown;
		copyLabel?: string;
		class?: string;
	}

	let {
		label,
		value,
		copyLabel = 'Details',
		class: className = ''
	}: Props = $props();

	let text = $derived(formatDetailValue(value));

	async function copyValue() {
		try {
			await navigator.clipboard.writeText(text);
			toast.success(`${copyLabel} copied to clipboard`, {
				duration: 2000,
				position: 'bottom-center'
			});
		} catch {
			toast.error(`Failed to copy ${copyLabel.toLowerCase()}`, {
				duration: 2000,
				position: 'bottom-center'
			});
		}
	}
</script>

<section class="overflow-hidden rounded-md border {className}">
	<div class="bg-muted/50 flex items-center justify-between gap-3 border-b px-2.5 py-1.5">
		<h3 class="text-muted-foreground text-[11px] font-semibold tracking-wide uppercase">{label}</h3>
		<button
			type="button"
			class="text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:ring-ring inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none"
			onclick={copyValue}
		>
			<span class="icon-[mdi--content-copy] h-3 w-3"></span>
			Copy
		</button>
	</div>
	<pre
		class="bg-muted/15 max-h-48 overflow-auto p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all focus-visible:outline-none">{text}</pre
	>
</section>

<script lang="ts">
	import { onMount } from 'svelte';
	import { cubicOut } from 'svelte/easing';
	import { fade, slide } from 'svelte/transition';

	let expanded = $state(false);

	interface Props {
		query: string;
	}

	let { query = $bindable() }: Props = $props();

	function toggleSearch() {
		expanded = !expanded;
		if (expanded) {
			requestAnimationFrame(() => {
				const input = document.getElementById('search-input');
				input?.focus();
			});
		}
	}

	onMount(() => {
		if (query !== '') {
			expanded = true;
		}
	});
</script>

<div class="relative shrink-0">
	<div
		class="bg-primary text-primary-foreground flex h-6 items-center overflow-hidden rounded-lg transition-[width] duration-300 ease-in-out"
		style="width: {expanded ? '16rem' : '1.5rem'}"
	>
		<button
			aria-label="Toggle Search"
			class="flex h-6 w-6 min-w-[1.5rem] shrink-0 items-center justify-center"
			onclick={toggleSearch}
		>
			<span class="icon-[mdi--magnify] h-5 w-5"></span>
		</button>

		{#if expanded}
			<input
				id="search-input"
				bind:value={query}
				type="text"
				placeholder={'Search...'}
				class="bg-primary ml-1 w-full text-sm leading-4 focus:outline-none"
				in:slide={{ duration: 250, easing: cubicOut, axis: 'x' }}
				out:fade={{ duration: 150 }}
				onkeydown={(e) => {
					if (e.key === 'Escape') {
						query = '';
						expanded = false;
					}
				}}
			/>
		{/if}
	</div>
</div>

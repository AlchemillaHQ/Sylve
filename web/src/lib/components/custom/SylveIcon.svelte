<script lang="ts">
	let { class: className = 'h-4 w-4' }: { class?: string } = $props();
</script>

<span class="inline-flex shrink-0 {className}" aria-hidden="true">
	<img src="/logo/black.svg" alt="" class="h-full w-full object-contain dark:hidden" />
	<img src="/logo/white.svg" alt="" class="hidden h-full w-full object-contain dark:block" />
</span>

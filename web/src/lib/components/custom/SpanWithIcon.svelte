<script lang="ts">
	interface Props {
		icon: string;
		size: string;
		gap: string;
		title: string;
		class?: string;
	}

	let { icon, size, gap, title, class: className = '' }: Props = $props();
</script>

<span class="flex items-center {gap} {className}">
	<span class={`${icon} ${size}`}></span>
	<span>{title}</span>
</span>

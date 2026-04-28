<script lang="ts">
	interface Props {
		icon: string;
		size: string;
		gap: string;
		title: string;
	}

	let { icon, size, gap, title }: Props = $props();
</script>

<span class="flex items-center {gap}">
	<span class={`${icon} ${size}`}></span>
	<span>{title}</span>
</span>

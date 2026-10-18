<script lang="ts">
	let { class: className = '' }: { class?: string } = $props();
</script>

<svg
	viewBox="12 14 96 92"
	fill="none"
	xmlns="http://www.w3.org/2000/svg"
	class={className}
	role="img"
	aria-label="Sad cat"
>
	<g class="text-muted-foreground">
		<path
			d="M33 44 L27 20 L53 31 Z"
			stroke="currentColor"
			stroke-width="3"
			stroke-linejoin="round"
			fill="currentColor"
			fill-opacity="0.08"
		/>
		<path
			d="M87 44 L93 20 L67 31 Z"
			stroke="currentColor"
			stroke-width="3"
			stroke-linejoin="round"
			fill="currentColor"
			fill-opacity="0.08"
		/>
		<circle
			cx="60"
			cy="67"
			r="33"
			fill="currentColor"
			fill-opacity="0.06"
			stroke="currentColor"
			stroke-width="3"
		/>
		<circle cx="47" cy="66" r="3.4" fill="currentColor" />
		<circle cx="73" cy="66" r="3.4" fill="currentColor" />
		<path d="M50 52 L39 58" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
		<path d="M70 52 L81 58" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
		<path d="M57 76 L63 76 L60 80 Z" fill="currentColor" />
		<path d="M53 88 Q60 81 67 88" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" />
		<path
			d="M25 68 H14 M25 74 H14"
			stroke="currentColor"
			stroke-width="2"
			stroke-linecap="round"
			opacity="0.45"
		/>
		<path
			d="M95 68 H106 M95 74 H106"
			stroke="currentColor"
			stroke-width="2"
			stroke-linecap="round"
			opacity="0.45"
		/>
		<ellipse cx="38" cy="78" rx="6" ry="3.6" fill="#fb7185" opacity="0.35" />
		<ellipse cx="82" cy="78" rx="6" ry="3.6" fill="#fb7185" opacity="0.35" />
	</g>
</svg>

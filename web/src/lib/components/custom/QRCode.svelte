<script lang="ts">
	import { onMount } from 'svelte';
	import QRCode from 'qrcode';
	import { watch } from 'runed';

	interface Props {
		id: string;
		value: string;
		logo?: string;
		size?: number;
		logoRatio?: number;
	}

	let { id, value, logo, size = 300, logoRatio = 0.2 }: Props = $props();
	let canvas: HTMLCanvasElement | null = $state(null);

	async function draw() {
		if (!canvas) return;
		if (!value) return;

		await QRCode.toCanvas(canvas, value, {
			width: size,
			errorCorrectionLevel: 'M'
		});

		const ctx = canvas ? canvas.getContext('2d') : null;
		if (!ctx) return;

		if (!logo) return;

		const img = new Image();
		img.src = logo;
		img.onload = () => {
			const logoSize = size * logoRatio;
			const padding = logoSize * 0.1;
			const bgSize = logoSize + padding * 2;
			const x = (size - bgSize) / 2;
			const y = (size - bgSize) / 2;
			const radius = bgSize * 0.2;

			ctx.fillStyle = '#ffffff';
			ctx.beginPath();
			ctx.moveTo(x + radius, y);
			ctx.arcTo(x + bgSize, y, x + bgSize, y + bgSize, radius);
			ctx.arcTo(x + bgSize, y + bgSize, x, y + bgSize, radius);
			ctx.arcTo(x, y + bgSize, x, y, radius);
			ctx.arcTo(x, y, x + bgSize, y, radius);
			ctx.closePath();
			ctx.fill();

			ctx.drawImage(img, x + padding, y + padding, logoSize, logoSize);
		};
	}

	onMount(async () => {
		draw();
	});

	watch(
		() => value,
		(value, prev) => {
			if (prev === undefined) {
				return;
			}

			draw();
		}
	);
</script>

<canvas {id} bind:this={canvas} width={size} height={size}></canvas>

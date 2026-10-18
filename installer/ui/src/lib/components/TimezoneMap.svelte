<script lang="ts">
	import { geoMiller } from 'd3-geo-projection';
	import { geoPath, geoContains, geoGraticule10 } from 'd3-geo';
	import { pointer, select, type Selection } from 'd3-selection';
	import { zoom as d3Zoom, zoomIdentity, type D3ZoomEvent } from 'd3-zoom';
	import { feature } from 'topojson-client';
	import world from 'world-atlas/countries-110m.json';
	import tzLookup from 'tz-lookup';
	import { watch } from 'runed';
	import { cn } from '$lib/utils';
	import Plus from '@lucide/svelte/icons/plus';
	import Minus from '@lucide/svelte/icons/minus';
	import Maximize from '@lucide/svelte/icons/maximize';

	let {
		value = 'UTC',
		label = '',
		onSelect
	}: { value?: string; label?: string; onSelect: (tz: string) => void } = $props();

	const W = 1000;
	const H = 500;
	const MIN_ZOOM = 1;
	const MAX_ZOOM = 6;
	const DEFAULT_ZOOM = 1.6;
	const DEFAULT_TRANSFORM = zoomIdentity
		.translate(W / 2, H / 2)
		.scale(DEFAULT_ZOOM)
		.translate(-W / 2, -H / 2);

	type CountryFeature = {
		type: string;
		geometry: never;
		properties: { name?: string };
	};

	const collection = feature(
		world as never,
		(world as unknown as { objects: { countries: never } }).objects.countries
	) as unknown as { features: CountryFeature[] };
	const countries = collection.features.filter((c) => c.properties?.name !== 'Antarctica');

	const frame = {
		type: 'Polygon',
		coordinates: [
			[
				[-180, -60],
				[180, -60],
				[180, 84],
				[-180, 84],
				[-180, -60]
			]
		]
	};
	const projection = geoMiller().fitExtent(
		[
			[0, 0],
			[W, H]
		],
		frame as never
	);
	const path = geoPath(projection);
	const graticulePath = path(geoGraticule10() as never) ?? '';
	const countryPaths = countries.map((c) => path(c as never) ?? '');

	const TZ_COORDS: Record<string, [number, number]> = {
		UTC: [0, 0],
		'Europe/London': [-0.13, 51.51],
		'Europe/Berlin': [13.4, 52.52],
		'Europe/Amsterdam': [4.9, 52.37],
		'Asia/Kolkata': [88.36, 22.57],
		'Asia/Singapore': [103.85, 1.29],
		'Asia/Tokyo': [139.69, 35.69],
		'America/New_York': [-74.0, 40.71],
		'America/Chicago': [-87.63, 41.88],
		'America/Los_Angeles': [-118.24, 34.05],
		'Australia/Sydney': [151.21, -33.87]
	};

	let transform = $state(DEFAULT_TRANSFORM);
	let svgSelection: Selection<SVGSVGElement, unknown, null, undefined> | null = null;
	let hovered = $state(-1);
	let selected = $state(-1);
	let markerGeo = $state<[number, number] | null>(TZ_COORDS['UTC']);
	let fromMap = $state('');

	const displayLabel = $derived(label || value);

	const zoomBehavior = d3Zoom<SVGSVGElement, unknown>()
		.scaleExtent([MIN_ZOOM, MAX_ZOOM])
		.extent([
			[0, 0],
			[W, H]
		])
		.translateExtent([
			[0, 0],
			[W, H]
		])
		.filter((event: MouseEvent | WheelEvent) => {
			const button = (event as MouseEvent).button;
			return (!event.ctrlKey || event.type === 'wheel') && (button === 0 || button === 1);
		})
		.on('zoom', (event: D3ZoomEvent<SVGSVGElement, unknown>) => {
			transform = event.transform;
		});

	function zoomable(node: SVGSVGElement) {
		const selection = select(node);
		svgSelection = selection;
		selection.call(zoomBehavior).on('dblclick.zoom', null);
		zoomBehavior.transform(selection, DEFAULT_TRANSFORM);
		return {
			destroy() {
				selection.on('.zoom', null);
				svgSelection = null;
			}
		};
	}

	function zoomBy(factor: number) {
		if (svgSelection) zoomBehavior.scaleBy(svgSelection, factor);
	}
	function resetZoom() {
		if (svgSelection) zoomBehavior.transform(svgSelection, DEFAULT_TRANSFORM);
	}

	const marker = $derived.by(() => {
		if (!markerGeo) return null;
		const point = projection(markerGeo);
		return point ? transform.apply(point as [number, number]) : null;
	});

	const labelWidth = $derived(Math.max(60, displayLabel.length * 10 + 22));

	const bubble = $derived.by(() => {
		if (!marker) return null;
		const [mx, my] = marker;
		const width = labelWidth;
		const x = Math.min(Math.max(mx, width / 2 + 6), W - width / 2 - 6);
		const below = my < 60;
		const y = below ? my + 18 : my - 42;
		return { x, y, width };
	});

	function coordsFor(tz: string): [number, number] | null {
		if (TZ_COORDS[tz]) return TZ_COORDS[tz];
		const etc = /^Etc\/GMT([+-]?\d+)$/.exec(tz);
		if (etc) return [-15 * Number(etc[1]), 0];
		if (tz === 'Etc/UTC' || tz === 'Etc/Universal' || tz === 'Etc/Zulu') return [0, 0];
		return null;
	}

	watch(
		() => value,
		(tz) => {
			if (tz === fromMap) return;
			const coords = coordsFor(tz);
			if (coords) markerGeo = coords;
		}
	);

	function locate(event: MouseEvent) {
		const svg = event.currentTarget as SVGSVGElement;
		const [x, y] = pointer(event, svg);
		const [ux, uy] = transform.invert([x, y]);
		const point = projection.invert?.([ux, uy]) ?? null;
		return { ux, uy, point };
	}

	function countryAt(point: [number, number]): number {
		for (let i = 0; i < countries.length; i++) {
			if (geoContains(countries[i] as never, point)) return i;
		}
		return -1;
	}

	function snapCountry(ux: number, uy: number): number {
		let best = -1;
		let bestDistance = Number.POSITIVE_INFINITY;
		for (let i = 0; i < countries.length; i++) {
			const [cx, cy] = path.centroid(countries[i] as never);
			if (!Number.isFinite(cx) || !Number.isFinite(cy)) continue;
			const distance = (cx - ux) ** 2 + (cy - uy) ** 2;
			if (distance < bestDistance) {
				bestDistance = distance;
				best = i;
			}
		}
		return best;
	}

	function onMove(event: MouseEvent) {
		const { point } = locate(event);
		hovered = point ? countryAt(point) : -1;
	}

	function onLeave() {
		hovered = -1;
	}

	function onClick(event: MouseEvent) {
		const { ux, uy, point } = locate(event);
		if (!point) return;
		const point2 = point as [number, number];
		const [lon, lat] = point2;
		let index = countryAt(point2);
		if (index < 0) index = snapCountry(ux, uy);
		selected = index;

		const tz = tzLookup(lat, lon);
		fromMap = tz;
		onSelect(tz);
		markerGeo = coordsFor(tz) ?? [lon, lat];
	}
</script>

<div class="space-y-2">
	<div class="relative overflow-hidden rounded-lg border border-border bg-muted/40">
		<svg
			use:zoomable
			viewBox="0 0 {W} {H}"
			preserveAspectRatio="xMidYMid slice"
			class="block aspect-[2/1] max-h-[min(340px,42vh)] w-full cursor-grab touch-none active:cursor-grabbing"
			role="presentation"
			onclick={onClick}
			onmousemove={onMove}
			onmouseleave={onLeave}
			onmousedown={(event) => {
				if (event.button === 1) event.preventDefault();
			}}
			onauxclick={(event) => {
				if (event.button === 1) event.preventDefault();
			}}
		>
			<g transform={transform.toString()}>
				<path d={graticulePath} class="fill-none stroke-border/70" stroke-width="0.4" />
				{#each countryPaths as d, index (index)}
					<path
						{d}
						class={cn(
							'transition-colors duration-150 hover:fill-primary/25',
							index === selected
								? 'fill-primary/40 stroke-primary/60'
								: index === hovered
									? 'fill-primary/25 stroke-primary/40'
									: 'fill-muted-foreground/25 stroke-background'
						)}
						stroke-width="0.5"
					/>
				{/each}
			</g>
			{#if marker && bubble}
				<g class="pointer-events-none animate-in fade-in zoom-in-50 duration-200">
					<rect
						x={bubble.x - bubble.width / 2}
						y={bubble.y}
						width={bubble.width}
						height="26"
						rx="6"
						class="fill-foreground/90"
					/>
					<text
						x={bubble.x}
						y={bubble.y + 17}
						text-anchor="middle"
						class="fill-background"
						style="font-size: 15px"
					>
						{displayLabel}
					</text>
					<circle cx={marker[0]} cy={marker[1]} r="9" class="fill-primary/20" />
					<circle
						cx={marker[0]}
						cy={marker[1]}
						r="4"
						class="fill-primary stroke-background"
						stroke-width="2"
					/>
				</g>
			{/if}
		</svg>

		<div class="absolute bottom-2 left-2 flex w-8 flex-col overflow-hidden rounded-md border border-border bg-background shadow-md">
			<div
				class="pointer-events-none border-b border-border px-0.5 py-1 text-center text-[10px] leading-none font-medium text-muted-foreground"
			>
				{Math.round(transform.k * 100)}%
			</div>
			<button
				class="grid h-8 w-full place-items-center transition-colors hover:bg-muted disabled:cursor-default disabled:opacity-35"
				onclick={() => zoomBy(1.6)}
				disabled={transform.k >= MAX_ZOOM}
				aria-label="Zoom in"
				title="Zoom in"
			>
				<Plus class="size-4" />
			</button>
			<div class="h-px bg-border"></div>
			<button
				class="grid h-8 w-full place-items-center transition-colors hover:bg-muted disabled:cursor-default disabled:opacity-35"
				onclick={() => zoomBy(1 / 1.6)}
				disabled={transform.k <= MIN_ZOOM}
				aria-label="Zoom out"
				title="Zoom out"
			>
				<Minus class="size-4" />
			</button>
			<div class="h-px bg-border"></div>
			<button
				class="grid h-8 w-full place-items-center transition-colors hover:bg-muted disabled:cursor-default disabled:opacity-35"
				onclick={resetZoom}
				disabled={transform.k <= MIN_ZOOM}
				aria-label="Reset zoom"
				title="Reset zoom"
			>
				<Maximize class="size-4" />
			</button>
		</div>
	</div>
	<p class="text-xs text-muted-foreground">
		Drag to pan, scroll or use the buttons to zoom. Click to pick a timezone.
	</p>
</div>

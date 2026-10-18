<script lang="ts">
	import { cn } from '$lib/utils';
	import type { DiskKind } from '$lib/pool';
	import HardDrive from '@lucide/svelte/icons/hard-drive';
	import MemoryStick from '@lucide/svelte/icons/memory-stick';
	import Server from '@lucide/svelte/icons/server';

	let { kind, class: className }: { kind: DiskKind; class?: string } = $props();
</script>

{#if kind === 'nvme'}
	<MemoryStick class={cn(className, 'text-cyan-600')} />
{:else if kind === 'ssd'}
	<HardDrive class={cn(className, 'text-blue-500')} />
{:else if kind === 'hdd'}
	<HardDrive class={cn(className, 'text-green-600')} />
{:else if kind === 'sdcard'}
	<MemoryStick class={cn(className, 'text-amber-600')} />
{:else}
	<Server class={cn(className, 'text-purple-500')} />
{/if}

<script lang="ts">
	import GuestConsole from '$lib/components/custom/Terminal/GuestConsole.svelte';
	import type { ContainerContext } from '$lib/types/container/container';
	import { getContext } from 'svelte';

	let { data }: { data: { node: string; ocId: number; hash: string } } = $props();

	const container = getContext<ContainerContext>('container');
	let runtime = $derived(container.current.runtime);
</script>

<GuestConsole
	kind="Container"
	name={container.current.name}
	url={`/api/container/${data.ocId}/console`}
	node={data.node}
	hash={data.hash}
	storageKey={`node-${data.node}-container-${data.ocId}-console`}
	status={runtime?.state === 'running' ? 'running' : runtime ? 'stopped' : 'unknown'}
	pending={!!container.pendingAction}
/>

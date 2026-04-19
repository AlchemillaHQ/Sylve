<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import * as Tabs from '$lib/components/ui/tabs/index.js';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	let basePath = $derived.by(() => {
		const marker = '/settings/system/notifications';
		const pathname = page.url.pathname;
		const index = pathname.indexOf(marker);
		if (index === -1) {
			return marker;
		}
		return pathname.slice(0, index + marker.length);
	});

	let activeTab = $derived.by(() => {
		if (page.url.pathname.endsWith('/rules')) {
			return 'rules';
		}
		return 'transports';
	});
</script>

<div class="flex h-full w-full flex-col">
	<Tabs.Root value={activeTab} class="w-full">
		<Tabs.List class="grid w-full grid-cols-2 p-0">
			<Tabs.Trigger value="transports" class="border-b" onclick={() => goto(`${basePath}/transports`)}>
				Transports
			</Tabs.Trigger>
			<Tabs.Trigger value="rules" class="border-b" onclick={() => goto(`${basePath}/rules`)}>
				Rules
			</Tabs.Trigger>
		</Tabs.List>
	</Tabs.Root>

	<div class="min-h-0 flex-1">
		{@render children?.()}
	</div>
</div>

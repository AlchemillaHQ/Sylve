<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import Header from '$lib/components/custom/Header.svelte';
	import BottomPanel from '$lib/components/skeleton/BottomPanel.svelte';
	import LeftPanel from '$lib/components/skeleton/LeftPanel.svelte';
	import * as Resizable from '$lib/components/ui/resizable';
	import LeftPanelClustered from './LeftPanelClustered.svelte';
	import { fade } from 'svelte/transition';
	import { resource, useInterval } from 'runed';
	import { storage } from '$lib';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	const clusterDetails = resource(
		[],
		async () => {
			return await getDetails();
		},
		{}
	);

	useInterval(() => 2000, {
		callback: () => {
			if (storage.visible) {
				clusterDetails.refetch();
			}
		}
	});

	let details = $derived(clusterDetails.current);
	let clustered = $derived(details?.cluster?.enabled || false);
</script>

<div class="flex min-h-screen w-full flex-col">
	<Header />
	<main class="flex flex-1 flex-col">
		<div class="h-[95vh] w-full md:h-[96vh]">
			<Resizable.PaneGroup
				direction="vertical"
				id="child-pane-auto"
				autoSaveId="child-pane-auto-save"
			>
				<Resizable.Pane>
					<Resizable.PaneGroup
						direction="horizontal"
						id="child-left-pane-auto"
						autoSaveId="child-left-pane-auto-save"
					>
						<Resizable.Pane defaultSize={12} class="border-l">
							<div transition:fade|global={{ duration: 400 }}>
								{#if clustered}
									<LeftPanelClustered />
								{:else}
									<LeftPanel />
								{/if}
							</div>
						</Resizable.Pane>
						<Resizable.Handle withHandle />

						<Resizable.Pane class="border-r">
							{@render children?.()}
						</Resizable.Pane>
					</Resizable.PaneGroup>
				</Resizable.Pane>

				<Resizable.Handle withHandle />

				<Resizable.Pane class="h-full min-h-20" defaultSize={10}>
					<BottomPanel />
				</Resizable.Pane>
			</Resizable.PaneGroup>
		</div>
	</main>
</div>

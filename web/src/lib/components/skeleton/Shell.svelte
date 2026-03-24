<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import Header from '$lib/components/custom/Header.svelte';
	import BottomPanel from '$lib/components/skeleton/BottomPanel.svelte';
	import LeftPanel from '$lib/components/skeleton/LeftPanel.svelte';
	import * as Resizable from '$lib/components/ui/resizable';
	import LeftPanelClustered from './LeftPanelClustered.svelte';
	import { fade } from 'svelte/transition';
	import { resource, watch } from 'runed';
	import { reload } from '$lib/stores/api.svelte';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	const clusterDetails = resource(
		() => 'cluster-details-shell',
		async () => {
			return await getDetails();
		},
		{}
	);

	watch(
		() => reload.clusterDetails,
		() => {
			if (reload.clusterDetails) {
				console.debug('Reloading cluster details due to reload.clusterDetails being true');
				clusterDetails.refetch();
				reload.clusterDetails = false;
			}
		}
	);

	let details = $derived(clusterDetails.current);
	let clustered = $derived(details?.cluster?.enabled || false);

	let leftPaneDefaultSize = $state(12);
	let topPaneDefaultSize = $state(90);
	let bottomPaneDefaultSize = $state(10);
	let lifecyclePaneActive = $state(false);

	const lifecyclePaneBoost = 6;

	function handleLifecycleActiveChange(active: boolean) {
		lifecyclePaneActive = active;
		bottomPaneDefaultSize = active ? 10 + lifecyclePaneBoost : 10;
		topPaneDefaultSize = 100 - bottomPaneDefaultSize;
	}
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
				<Resizable.Pane defaultSize={topPaneDefaultSize}>
					<Resizable.PaneGroup
						direction="horizontal"
						id="child-left-pane-auto"
						autoSaveId="child-left-pane-auto-save"
					>
						<Resizable.Pane defaultSize={leftPaneDefaultSize} class="border-l">
							<div class="h-full" transition:fade|global={{ duration: 400 }}>
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

				<Resizable.Pane class="h-full min-h-20" defaultSize={bottomPaneDefaultSize}>
					<BottomPanel {clustered} onLifecycleActiveChange={handleLifecycleActiveChange} />
				</Resizable.Pane>
			</Resizable.PaneGroup>
		</div>
	</main>
</div>

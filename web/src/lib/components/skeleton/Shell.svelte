<script lang="ts">
	import { getDetails } from '$lib/api/cluster/cluster';
	import Header from '$lib/components/custom/Header.svelte';
	import BottomPanel from '$lib/components/skeleton/BottomPanel.svelte';
	import LeftPanel from '$lib/components/skeleton/LeftPanel.svelte';
	import * as Resizable from '$lib/components/ui/resizable';
	import {
		DEFAULT_RESOURCE_TREE_PREFERENCES,
		normalizeResourceTreePreferences,
		type ResourceTreePreferences
	} from '$lib/resource-tree';
	import LeftPanelClustered from './LeftPanelClustered.svelte';
	import ResourceTreeToolbar from './ResourceTreeToolbar.svelte';
	import { fade } from 'svelte/transition';
	import { PersistedState, resource, watch } from 'runed';
	import { reload } from '$lib/stores/api.svelte';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import type { ActiveLifecycleGuest } from '$lib/types/task/lifecycle';
	import { isAPIResponse } from '$lib/utils/http';

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();

	const clusterDetails = resource(
		() => 'cluster-details-shell',
		async () => {
			const result = await getDetails();
			return isAPIResponse(result) ? null : result;
		},
		{ initialValue: null as ClusterDetails | null }
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
	let clustered = $derived(details?.cluster?.enabled === true);
	const resourceTreePreferences = new PersistedState<ResourceTreePreferences>(
		'sylve-resource-tree-preferences-v1',
		DEFAULT_RESOURCE_TREE_PREFERENCES
	);
	let treePreferences = $derived(normalizeResourceTreePreferences(resourceTreePreferences.current));

	let treeSearchQuery = $state('');
	let leftPanelRef = $state<{ expandAll: () => void; collapseAll: () => void } | undefined>();
	let leftPanelClusteredRef = $state<
		{ expandAll: () => void; collapseAll: () => void } | undefined
	>();
	let lifecycleActive = $state(false);
	let activeLifecycleGuests = $state.raw<ActiveLifecycleGuest[]>([]);

	function expandTree() {
		(clustered ? leftPanelClusteredRef : leftPanelRef)?.expandAll();
	}

	function collapseTree() {
		(clustered ? leftPanelClusteredRef : leftPanelRef)?.collapseAll();
	}

	let leftPaneDefaultSize = $state(12);
	let topPaneDefaultSize = $state(90);
	let bottomPaneDefaultSize = $state(10);

	const lifecyclePaneBoost = 6;

	function handleLifecycleActiveChange(active: boolean, activeGuests: ActiveLifecycleGuest[]) {
		activeLifecycleGuests = activeGuests;
		if (lifecycleActive === active) return;

		lifecycleActive = active;
		bottomPaneDefaultSize = active ? 10 + lifecyclePaneBoost : 10;
		topPaneDefaultSize = 100 - bottomPaneDefaultSize;
	}

	function updateTreePreferences(preferences: ResourceTreePreferences) {
		resourceTreePreferences.current = normalizeResourceTreePreferences(preferences);
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
						<Resizable.Pane defaultSize={leftPaneDefaultSize} minSize={12} class="border-l">
							<div class="flex h-full min-h-0 flex-col">
								<ResourceTreeToolbar
									preferences={treePreferences}
									onChange={updateTreePreferences}
									searchQuery={treeSearchQuery}
									onSearchChange={(value) => (treeSearchQuery = value)}
									onExpandAll={expandTree}
									onCollapseAll={collapseTree}
								/>
								<div class="min-h-0 flex-1" transition:fade|global={{ duration: 400 }}>
									{#if clustered}
										<LeftPanelClustered
											bind:this={leftPanelClusteredRef}
											preferences={treePreferences}
											searchQuery={treeSearchQuery}
											{activeLifecycleGuests}
										/>
									{:else}
										<LeftPanel
											bind:this={leftPanelRef}
											preferences={treePreferences}
											searchQuery={treeSearchQuery}
											{activeLifecycleGuests}
										/>
									{/if}
								</div>
							</div>
						</Resizable.Pane>
						<Resizable.Handle withHandle gripPreferenceKey="shell-resource-tree" />

						<Resizable.Pane class="border-r">
							{@render children?.()}
						</Resizable.Pane>
					</Resizable.PaneGroup>
				</Resizable.Pane>

				<Resizable.Handle withHandle gripPreferenceKey="shell-bottom-panel" />

				<Resizable.Pane class="h-full min-h-20" defaultSize={bottomPaneDefaultSize}>
					<BottomPanel {clustered} onLifecycleActiveChange={handleLifecycleActiveChange} />
				</Resizable.Pane>
			</Resizable.PaneGroup>
		</div>
	</main>
</div>

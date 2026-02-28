<script lang="ts">
	import { storage } from '$lib';
	import { getDetails } from '$lib/api/cluster/cluster';
	import TreeView from '$lib/components/custom/TreeView.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Resizable from '$lib/components/ui/resizable';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import type { ClusterDetails } from '$lib/types/cluster/cluster';
	import { isAPIResponse } from '$lib/utils/http';
	import { resource } from 'runed';

	let openCategories: { [key: string]: boolean } = $state({});

	const toggleCategory = (label: string) => {
		openCategories = { ...openCategories, [label]: !openCategories[label] };
	};

	interface NodeItem {
		label: string;
		icon: string;
		href?: string;
		children?: NodeItem[];
	}

	let clusterDetails = resource(
		() => 'cluster-details-layout',
		async () => {
			const res = await getDetails();
			if (isAPIResponse(res)) {
				return null;
			}
			return res;
		},
		{ initialValue: null as ClusterDetails | null }
	);

	let clusterEnabled = $derived(clusterDetails.current?.cluster?.enabled === true);

	let nodeItems: NodeItem[] = $derived.by(() => {
		const items: NodeItem[] = [
			{
				label: 'Summary',
				icon: 'basil--document-outline',
				href: '/datacenter/summary'
			},
			{
				label: 'Notes',
				icon: 'mdi--notes',
				href: '/datacenter/notes'
			},
			{
				label: 'Cluster',
				icon: 'carbon--assembly-cluster',
				href: '/datacenter/cluster'
			},
			{
				label: 'Backups',
				icon: 'mdi--backup-restore',
				children: [
					{
						label: 'Targets',
						icon: 'mdi--server-network',
						href: '/datacenter/backups/targets'
					},
					{
						label: 'Jobs',
						icon: 'mdi--calendar-sync',
						href: '/datacenter/backups/jobs'
					},
					{
						label: 'Events',
						icon: 'mdi--history',
						href: '/datacenter/backups/events'
					}
				]
			}
			// {
			// 	label: 'Storage',
			// 	icon: 'mdi--storage',
			// 	href: '/datacenter/storage'
			// }
		];

		if (clusterEnabled) {
			items.push({
				label: 'Replication',
				icon: 'mdi--sync',
				children: [
					{
						label: 'Policies',
						icon: 'mdi--clipboard-list-outline',
						href: '/datacenter/replication/policies'
					},
					{
						label: 'Events',
						icon: 'mdi--history',
						href: '/datacenter/replication/events'
					}
				]
			});
		}

		return items;
	});

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<span>Data Center</span>
		<div>
			<Button
				size="sm"
				class="h-6"
				onclick={() => window.open('https://discord.gg/bJB826JvXK', '_blank')}
				title="Discord"
			>
				<div class="flex items-center">
					<span class="icon-[lucide--circle-help] mr-2 h-5 w-5"></span>
					<span>Help</span>
				</div>
			</Button>

			<Button
				size="sm"
				class="h-6"
				onclick={() => {
					storage.openAbout = true;
				}}
				title="Sponsor"
			>
				<div class="flex items-center">
					<span class="icon-[mdi--heart] h-5 w-5"></span>
				</div>
			</Button>
		</div>
	</div>

	<Resizable.PaneGroup
		direction="horizontal"
		class="h-full w-full"
		id="main-pane-auto"
		autoSaveId="main-pane-auto-save"
	>
		<Resizable.Pane defaultSize={15}>
			<div class="h-full px-1.5">
				<div class="h-full overflow-y-auto">
					<nav aria-label="Difuse-sidebar" class="menu thin-scrollbar w-full">
						<ul>
							<ScrollArea orientation="both" class="h-full w-full">
								{#each nodeItems as item}
									<TreeView {item} onToggle={toggleCategory} />
								{/each}
							</ScrollArea>
						</ul>
					</nav>
				</div>
			</div>
		</Resizable.Pane>

		<Resizable.Handle withHandle />

		<Resizable.Pane>
			<div class="h-full overflow-auto">
				{@render children?.()}
			</div>
		</Resizable.Pane>
	</Resizable.PaneGroup>
</div>

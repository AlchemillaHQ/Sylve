<script lang="ts">
	import TreeView from '$lib/components/custom/TreeView.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Resizable from '$lib/components/ui/resizable';
	import { ScrollArea } from '$lib/components/ui/scroll-area/index.js';
	import CircleHelp from 'lucide-svelte/icons/circle-help';

	let openCategories: { [key: string]: boolean } = $state({});

	const toggleCategory = (label: string) => {
		openCategories[label] = !openCategories[label];
	};

	interface NodeItem {
		label: string;
		icon: string;
		href?: string;
		children?: NodeItem[];
	}

	let nodeItems: NodeItem[] = $derived.by(() => {
		return [
			{
				label: 'Summary',
				icon: 'basil:document-outline',
				href: '/datacenter/summary'
			},
			{
				label: 'Notes',
				icon: 'mdi:notes',
				href: '/datacenter/notes'
			},
			{
				label: 'Cluster',
				icon: 'carbon:assembly-cluster',
				href: '/datacenter/cluster'
			}
		];
	});

	interface Props {
		children?: import('svelte').Snippet;
	}

	let { children }: Props = $props();
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center justify-between border-b p-2">
		<span>Data Center</span>

		<Button
			size="sm"
			class="h-6"
			onclick={() => (window.location.href = 'https://github.com/AlchemillaHQ/Sylve')}
		>
			<div class="flex items-center">
				<CircleHelp class="mr-2 h-5 w-5" />

				<span>Help</span>
			</div>
		</Button>
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
									<TreeView {item} onToggle={toggleCategory} bind:this={openCategories} />
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

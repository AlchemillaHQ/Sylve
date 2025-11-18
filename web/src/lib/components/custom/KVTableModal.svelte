<script lang="ts">
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import Icon from '@iconify/svelte';

	interface Props {
		open: boolean;
		titles: {
			icon?: string;
			main: string;
			key: string;
			value: string;
		};
		type: string;
		KV:
			| Record<string, string | number | Record<string, string | number>>
			| Array<Record<string, string | number>>;
		actions: {
			close: () => void;
		};
	}

	let { open, titles, type, KV, actions }: Props = $props();

	let tableHeaders = $derived.by(() => {
		if (Array.isArray(KV)) {
			return Object.keys(KV[0]);
		} else {
			return [];
		}
	});

	let expandedObjects: Record<string, boolean> = $state({});

	function toggleObjectExpansion(key: string) {
		expandedObjects[key] = !expandedObjects[key];
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="flex max-h-[80vh] w-[90%] flex-col gap-0 overflow-hidden p-5 lg:max-w-4xl"
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<div class="flex items-center justify-between">
			<div class="flex items-center">
				{#if titles.icon}
					<Icon icon={titles.icon} class="h-6 w-6" />
				{/if}
				<h2 class="ml-2 text-lg font-semibold">{titles.main}</h2>
			</div>

			<Dialog.Close
				class="flex h-6 w-6 items-center justify-center rounded-sm opacity-70 transition-opacity hover:opacity-100"
				onclick={actions.close}
			>
				<Icon icon="material-symbols:close-rounded" class="h-6 w-6" />
			</Dialog.Close>
		</div>

		<div class="mt-2 max-h-[60vh] overflow-y-auto">
			<Table.Root class="w-full table-auto border-collapse">
				<Table.Header class="bg-background sticky top-0 z-[50]">
					<Table.Row>
						{#if tableHeaders.length > 0}
							{#each tableHeaders as header}
								<Table.Head class="h-10 px-3 py-2">{header}</Table.Head>
							{/each}
						{:else}
							<Table.Head class="h-10 px-3 py-2">{titles.key}</Table.Head>
							<Table.Head class="h-10 px-3 py-2">{titles.value}</Table.Head>
						{/if}
					</Table.Row>
				</Table.Header>

				<Table.Body>
					{#if tableHeaders.length > 0}
						{#each KV as Array<Record<string, string | number>> as row}
							<Table.Row>
								{#each tableHeaders as header}
									<Table.Cell class="h-10 px-3 py-2">{row[header]}</Table.Cell>
								{/each}
							</Table.Row>
						{/each}
					{:else}
						{#each Object.entries(KV) as [key, value]}
							{#if typeof value === 'object' && value !== null && !Array.isArray(value)}
								<Table.Row>
									<Table.Cell class="h-10 w-1/2 px-1 py-2 font-medium whitespace-nowrap">
										<button
											class="flex w-full items-center gap-1 text-left"
											onclick={() => toggleObjectExpansion(key)}
										>
											<Icon
												icon={expandedObjects[key]
													? 'material-symbols:keyboard-arrow-down'
													: 'material-symbols:keyboard-arrow-right'}
												class="h-4 w-4 opacity-70"
											/>
											{key}
										</button>
									</Table.Cell>
									<Table.Cell class="h-10 px-3 py-2 italic opacity-50">
										Object ({Object.keys(value).length} properties)
									</Table.Cell>
								</Table.Row>
								{#if expandedObjects[key]}
									{#each Object.entries(value) as [nestedKey, nestedValue]}
										<Table.Row>
											<Table.Cell class="h-10 py-2 pr-3 pl-8 opacity-90">
												{nestedKey}
											</Table.Cell>
											<Table.Cell class="h-10 px-3 py-2">
												{nestedValue}
											</Table.Cell>
										</Table.Row>
									{/each}
								{/if}
							{:else}
								<Table.Row>
									<Table.Cell class="h-10 px-3 py-2">{key}</Table.Cell>
									<Table.Cell class="h-10 px-3 py-2">{value}</Table.Cell>
								</Table.Row>
							{/if}
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>
		</div>
	</Dialog.Content>
</Dialog.Root>

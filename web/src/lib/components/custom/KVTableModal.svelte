<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';

	interface Props {
		open: boolean;
		titles: {
			icon?: string;
			main: string;
			key: string;
			value: string;
		};
		KV:
			| Record<string, string | number | Record<string, string | number>>
			| Array<Record<string, string | number>>;
	}

	let { open = $bindable(), titles, KV }: Props = $props();

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
		showCloseButton={true}
		onInteractOutside={(e) => e.preventDefault()}
		onEscapeKeydown={(e) => e.preventDefault()}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon={titles.icon ? `icon-[${titles.icon}]` : ''}
					size="h-6 w-6"
					gap="gap-2"
					title={titles.main}
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-2 max-h-[60vh] overflow-y-auto">
			<Table.Root
				class="w-full border-collapse {tableHeaders.length > 0 ? 'table-auto' : 'table-fixed'}"
			>
				<Table.Header class="bg-background sticky top-0 z-50">
					<Table.Row>
						{#if tableHeaders.length > 0}
							{#each tableHeaders as header (header)}
								<Table.Head class="h-10 px-3 py-2">{header}</Table.Head>
							{/each}
						{:else}
							<Table.Head class="h-10 w-1/3 px-3 py-2">{titles.key}</Table.Head>
							<Table.Head class="h-10 w-2/3 px-3 py-2">{titles.value}</Table.Head>
						{/if}
					</Table.Row>
				</Table.Header>

				<Table.Body>
					{#if tableHeaders.length > 0}
						{#each KV as Array<Record<string, string | number>> as row (row)}
							<Table.Row>
								{#each tableHeaders as header (header)}
									<Table.Cell class="h-10 px-3 py-2 whitespace-pre-line">{row[header]}</Table.Cell>
								{/each}
							</Table.Row>
						{/each}
					{:else}
						{#each Object.entries(KV) as [key, value] (key)}
							{#if typeof value === 'object' && value !== null && !Array.isArray(value)}
								<Table.Row>
									<Table.Cell class="h-10 w-1/2 px-1 py-2 font-medium whitespace-nowrap">
										<button
											class="flex w-full items-center gap-1 text-left"
											onclick={() => toggleObjectExpansion(key)}
										>
											<span
												class="icon-[{expandedObjects[key]
													? 'material-symbols--keyboard-arrow-down'
													: 'material-symbols--keyboard-arrow-right'}] h-6 w-6"
											></span>
											{key}
										</button>
									</Table.Cell>
									<Table.Cell class="h-10 px-3 py-2 italic opacity-50">
										Object ({Object.keys(value).length} properties)
									</Table.Cell>
								</Table.Row>
								{#if expandedObjects[key]}
									{#each Object.entries(value) as [nestedKey, nestedValue] (nestedKey)}
										<Table.Row>
											<Table.Cell class="py-2 pr-3 pl-8 opacity-90">
												{nestedKey}
											</Table.Cell>
											<Table.Cell class="px-3 py-2 wrap-break-word whitespace-pre-line">
												{nestedValue}
											</Table.Cell>
										</Table.Row>
									{/each}
								{/if}
							{:else}
								<Table.Row>
									<Table.Cell class="px-3 py-2">{key}</Table.Cell>
									<Table.Cell class="px-3 py-2 wrap-break-word whitespace-pre-line"
										>{value}</Table.Cell
									>
								</Table.Row>
							{/if}
						{/each}
					{/if}
				</Table.Body>
			</Table.Root>
		</div>
	</Dialog.Content>
</Dialog.Root>

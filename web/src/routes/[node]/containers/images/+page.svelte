<script lang="ts">
	import {
		deleteContainerImage,
		getContainerImages,
		pruneContainerImages
	} from '$lib/api/container/resources';
	import PullImage from '$lib/components/custom/Container/PullImage.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { ContainerImage } from '$lib/types/container/container';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { describeContainerError, toastContainerError } from '$lib/utils/container/errors';
	import { escapeHTML } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let pool = $derived(data.pools[0] ?? '');
	const images = resource(
		[() => data.node, () => pool],
		async ([node, pool]) => (pool ? await getContainerImages(pool, node) : []),
		{ initialValue: [] as ContainerImage[] }
	);

	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? images.current.find((image) => image.id === activeRows?.[0].id)
			: undefined
	);
	let modals = $state({ pull: false, delete: false, prune: false });

	const imageName = (image: ContainerImage) => image.names[0] ?? image.id.slice(0, 12);

	let table = $derived({
		columns: [
			{
				field: 'name',
				title: 'Name',
				formatter: (cell: CellComponent) => {
					const name = escapeHTML(cell.getValue());
					return cell.getRow().getData().untagged
						? `<span class="text-muted-foreground">${name}</span> <span class="ml-1 rounded border px-1 text-xs" title="A newer pull of this tag replaced it; Prune removes it">Untagged</span>`
						: name;
				}
			},
			{
				field: 'id',
				title: 'ID',
				formatter: (cell: CellComponent) => String(cell.getValue()).slice(0, 12)
			},
			{
				field: 'size',
				title: 'Size',
				formatter: (cell: CellComponent) => formatBytesBinary(cell.getValue())
			},
			{
				field: 'created',
				title: 'Created',
				formatter: (cell: CellComponent) => convertDbTime(cell.getValue())
			}
		],
		rows: images.current.map((image) => ({
			id: image.id,
			name: image.names[0] ?? (image.previous || '<none>'),
			untagged: image.names.length === 0,
			size: image.size,
			created: image.created
		}))
	});

	async function remove() {
		if (!selected) return;
		const name = imageName(selected);
		const result = await deleteContainerImage(pool, selected.id, data.node);
		if (result.status === 'error') {
			toastContainerError(result);
			return;
		}

		toast.success(`Image ${name} deleted`, { position: 'bottom-center' });
		activeRows = null;
		await images.refetch();
	}

	async function prune() {
		const result = await pruneContainerImages(pool, data.node);
		modals.prune = false;
		if (result.status === 'error') {
			toastContainerError(result);
			return;
		}

		toast.success('Unused images pruned', { position: 'bottom-center' });
		await images.refetch();
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<SimpleSelect
			placeholder="No usable pools"
			icon="icon-[mdi--database-outline]"
			options={data.pools.map((name) => ({ value: name, label: name }))}
			value={pool}
			onChange={(value) => (pool = value)}
			classes={{ parent: 'w-40', trigger: '!h-6 w-full gap-1.5 px-2.5 text-sm' }}
		/>
		<Button size="sm" class="h-6" disabled={!pool} onclick={() => (modals.pull = true)}>
			<SpanWithIcon icon="icon-[mdi--download]" size="h-4 w-4" gap="gap-2" title="Pull" />
		</Button>
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={!pool}
			onclick={() => (modals.prune = true)}
		>
			<SpanWithIcon icon="icon-[mdi--broom]" size="h-4 w-4" gap="gap-2" title="Prune" />
		</Button>
		{#if selected}
			<Button size="sm" variant="outline" class="h-6.5" onclick={() => (modals.delete = true)}>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
			</Button>
		{/if}
	</div>

	{#if images.error}
		{@const problem = describeContainerError(images.error.message)}
		<p class="text-destructive px-3 py-2 text-xs" role="alert">
			{problem.title}{problem.detail ? `: ${problem.detail}` : ''}
		</p>
	{/if}

	<TreeTable
		data={table}
		name="tt-container-images"
		multipleSelect={false}
		bind:parentActiveRow={activeRows}
		bind:query
	/>
</div>

{#if modals.pull}
	<PullImage bind:open={modals.pull} node={data.node} {pool} afterChange={() => images.refetch()} />
{/if}

<AlertDialog
	bind:open={modals.delete}
	names={{ parent: 'image', element: selected ? imageName(selected) : '' }}
	actions={{ onConfirm: remove, onCancel: () => (modals.delete = false) }}
/>

<AlertDialog
	bind:open={modals.prune}
	description={`Remove every image on ${pool} that no container uses?`}
	confirmLabel="Prune"
	keepOpenOnConfirm
	actions={{ onConfirm: prune, onCancel: () => (modals.prune = false) }}
/>

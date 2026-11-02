<script lang="ts">
	import { getMountableDatasets } from '$lib/api/container/resources';
	import EditDialog from '$lib/components/custom/Container/EditDialog.svelte';
	import Mount from '$lib/components/custom/Container/Mount.svelte';
	import MountSuggestions from '$lib/components/custom/Container/MountSuggestions.svelte';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import {
		containerSpecOf,
		mountTypeLabels,
		type ContainerContext,
		type ContainerMount
	} from '$lib/types/container/container';
	import { resource } from 'runed';
	import { getContext } from 'svelte';

	let { data }: { data: { node: string } } = $props();

	const container = getContext<ContainerContext>('container');
	let current = $derived(container.current);
	let disabled = $derived(container.busy);

	const datasets = resource(
		() => data.node,
		(hostname) => getMountableDatasets(hostname),
		{ initialValue: [] }
	);

	const columns: Column[] = [
		{ title: 'Type', field: 'type' },
		{ title: 'Source', field: 'source' },
		{ title: 'Target', field: 'target' },
		{ title: 'Access', field: 'access' }
	];
	let rows = $derived(
		current.mounts.map((mount, index) => ({
			id: index,
			type: mountTypeLabels[mount.type],
			source:
				mount.type === 'volume'
					? `${mount.source} (${current.pool}/sylve/containers/${current.ocId}/${mount.source})`
					: mount.source,
			target: mount.target,
			access: mount.readOnly ? 'Read-only' : 'Read-write'
		}))
	);

	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(activeRows?.length === 1 ? Number(activeRows[0].id) : null);
	let query = $state('');

	let dialog = $state({
		open: false,
		index: -1,
		mount: { type: 'volume', source: '', target: '', readOnly: false } as ContainerMount
	});
	let deleting = $state(false);

	function edit(index: number, mount?: ContainerMount) {
		dialog = {
			open: true,
			index,
			mount:
				index >= 0
					? { ...current.mounts[index] }
					: (mount ?? { type: 'volume', source: '', target: '', readOnly: false })
		};
	}

	function save() {
		const spec = containerSpecOf(current);
		if (dialog.index < 0) spec.mounts.push(dialog.mount);
		else spec.mounts[dialog.index] = dialog.mount;
		return container.update(spec);
	}

	async function remove() {
		const spec = containerSpecOf(current);
		spec.mounts.splice(selected!, 1);
		if (await container.update(spec)) activeRows = null;
		deleting = false;
	}
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Button size="sm" class="h-6" {disabled} onclick={() => edit(-1)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{#if selected !== null}
			<Button size="sm" variant="outline" class="h-6.5" {disabled} onclick={() => edit(selected!)}>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
			</Button>
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				{disabled}
				onclick={() => (deleting = true)}
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Remove" />
			</Button>
		{/if}
	</div>

	{#if current.catalogApp && !disabled}
		<MountSuggestions
			catalogApp={current.catalogApp}
			node={data.node}
			mounts={current.mounts}
			onAdd={(mount) => edit(-1, mount)}
			class="m-2"
		/>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={{ columns, rows }}
			name="container-storage-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if dialog.open}
	<EditDialog
		bind:open={dialog.open}
		title={dialog.index < 0 ? 'New Mount' : 'Edit Mount'}
		icon="icon-[mdi--harddisk]"
		width="sm:max-w-2xl"
		onSave={save}
	>
		<div class="grid grid-cols-[9rem_1fr_1fr_auto] items-center gap-2">
			<span class="text-muted-foreground text-sm font-medium">Type</span>
			<span class="text-muted-foreground text-sm font-medium">Source</span>
			<span class="text-muted-foreground text-sm font-medium">Target</span>
			<span></span>
			<Mount bind:mount={dialog.mount} datasets={datasets.current} />
		</div>
	</EditDialog>
{/if}

<AlertDialog
	bind:open={deleting}
	customTitle="Remove this mount? The container is recreated without it. A volume's data stays on its dataset."
	actions={{ onConfirm: remove, onCancel: () => (deleting = false) }}
/>

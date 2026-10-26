<script lang="ts">
	import { storage } from '$lib';
	import { deleteNFSShare, getNFSShares, getNFSStatus } from '$lib/api/nfs';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Editor from '$lib/components/custom/NFS/Share.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import { NFSMutationSchema, nfsShareStatus, type NFSShare, type NFSStatus } from '$lib/types/nfs';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { useInterval, watch } from 'runed';
	import { onDestroy, onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();
	const initial = untrack(() => data);
	let shares = $state(initial.shares);
	let status = $state<NFSStatus | null>(null);
	let datasets = $state(initial.datasets);
	let sharesLoading = $state(initial.shares.length === 0);
	let loadFailed = $state(false);
	let statusFailed = $state(false);
	let sharesVersion: string | null = null;
	let query = $state('');
	let activeRows = $state<Row[] | null>(null);
	let selected = $derived(
		activeRows?.length === 1
			? shares.find((share) => share.id === Number(activeRows?.[0].id))
			: undefined
	);
	let editorOpen = $state(false);
	let editing = $state<NFSShare | null>(null);
	let deleteOpen = $state(false);
	let reload = $state(false);
	let refreshing = $state(false);
	let polling = $state(false);
	let removing = $state(false);
	let generation = 0;
	let reads: AbortController | null = null;
	onDestroy(() => {
		generation++;
		reads?.abort();
	});
	onMount(() => {
		void refresh(true);
	});
	let disabled = $derived(loadFailed || refreshing || removing);
	watch.pre(
		() => data,
		(next) => {
			generation++;
			reads?.abort();
			refreshing = false;
			polling = false;
			removing = false;
			reload = false;
			shares = next.shares;
			status = null;
			datasets = next.datasets;
			sharesLoading = next.shares.length === 0;
			loadFailed = false;
			statusFailed = false;
			sharesVersion = null;
			activeRows = null;
			editing = null;
			editorOpen = false;
			deleteOpen = false;
			query = '';
			void refresh(true);
		}
	);
	async function refresh(background = false) {
		if (refreshing || polling || (background && (removing || reload || editorOpen || deleteOpen)))
			return;
		if (background) polling = true;
		else refreshing = true;
		const node = data.node;
		const currentGeneration = generation;
		reads = new AbortController();
		const canApply = () =>
			generation === currentGeneration &&
			(!background || (!removing && !reload && !editorOpen && !deleteOpen));
		try {
			const nextStatus = await getNFSStatus({ hostname: node, signal: reads.signal });
			if (!canApply()) return;
			status = nextStatus;
			statusFailed = false;
			if (!background || loadFailed || sharesVersion !== nextStatus.sharesVersion) {
				try {
					const result = await getNFSShares({ hostname: node, signal: reads.signal });
					if (!canApply()) return;
					shares = result;
					sharesVersion = nextStatus.sharesVersion;
					loadFailed = false;
					void updateCache('nfs-shares', result, node);
				} catch {
					if (canApply()) loadFailed = true;
				} finally {
					if (generation === currentGeneration) sharesLoading = false;
				}
			}
		} catch {
			if (canApply()) {
				status = null;
				statusFailed = true;
				sharesLoading = false;
			}
		} finally {
			if (generation === currentGeneration) {
				if (background) polling = false;
				else refreshing = false;
			}
		}
	}
	async function remove() {
		if (!selected || disabled) return;
		const node = data.node;
		const currentGeneration = generation;
		removing = true;
		try {
			const result = await deleteNFSShare(selected.id, { hostname: node });
			if (generation !== currentGeneration) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			const mutation = NFSMutationSchema.safeParse(result.data);
			if (mutation.success && (mutation.data.applyStatus === 'stopped' || mutation.data.applied)) {
				toast.success('NFS share deleted', { position: 'bottom-center' });
			} else {
				toast.warning('NFS share deleted; apply pending.', {
					position: 'bottom-center'
				});
			}
			deleteOpen = false;
			activeRows = null;
			reload = true;
		} finally {
			if (generation === currentGeneration) removing = false;
		}
	}
	watch(
		[() => reload, () => refreshing, () => polling, () => removing],
		([requested, refreshing, polling, removing]) => {
			if (requested && !refreshing && !polling && !removing) {
				reload = false;
				void refresh();
			}
		}
	);
	watch(
		() => storage.visible,
		(visible) => {
			if (visible) void refresh(true);
		}
	);
	useInterval(5000, {
		callback: () => {
			if (storage.visible) void refresh(true);
		}
	});
	const statusStyles: Record<ReturnType<typeof nfsShareStatus>, { icon: string; color: string }> = {
		Applied: { icon: 'mdi:check-circle', color: 'text-green-500' },
		Disabled: { icon: 'mdi:close-circle', color: 'text-muted-foreground' },
		Inactive: { icon: 'mdi:pause-circle-outline', color: 'text-muted-foreground' },
		Pending: { icon: 'mdi:alert-circle-outline', color: 'text-amber-500' },
		Applying: { icon: 'mdi:loading', color: 'text-sky-500' },
		Unavailable: { icon: 'mdi:alert-circle', color: 'text-red-500' },
		Unknown: { icon: 'mdi:help-circle-outline', color: 'text-muted-foreground' }
	};
	function badge(label: string, color: string) {
		return `<span class="inline-flex items-center rounded border px-1 text-xs font-mono leading-tight ${color}">${escapeHTML(label)}</span>`;
	}
	let table = $derived({
		columns: [
			{ field: 'id', title: 'ID', visible: false },
			{
				field: 'enabled',
				title: 'Enabled',
				formatter: (cell: CellComponent) =>
					cell.getValue()
						? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
						: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
			},
			{
				field: 'effective',
				title: 'Status',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue() as ReturnType<typeof nfsShareStatus>;
					const style = statusStyles[value];
					return renderWithIcon(style.icon, value, style.color);
				}
			},
			{ field: 'description', title: 'Description', formatter: 'plaintext' },
			{ field: 'dataset', title: 'Filesystem', formatter: 'plaintext' },
			{ field: 'mountpoint', title: 'Mountpoint', formatter: 'plaintext' },
			{
				field: 'access',
				title: 'Access',
				formatter: (cell: CellComponent) =>
					badge(
						cell.getValue(),
						cell.getValue() === 'Read Only'
							? 'text-sky-500 border-sky-500/50'
							: 'text-amber-500 border-amber-500/50'
					)
			},
			{
				field: 'security',
				title: 'Security',
				formatter: (cell: CellComponent) => {
					const colors: Record<NFSShare['security'][number], string> = {
						sys: 'text-muted-foreground border-muted-foreground/50',
						krb5: 'text-sky-500 border-sky-500/50',
						krb5i: 'text-violet-500 border-violet-500/50',
						krb5p: 'text-emerald-500 border-emerald-500/50'
					};
					return `<span class="inline-flex items-center gap-1">${(
						cell.getValue() as NFSShare['security']
					)
						.map((flavor) => badge(flavor.toUpperCase(), colors[flavor]))
						.join('')}</span>`;
				}
			},
			{ field: 'mapping', title: 'Mapping', formatter: 'plaintext' },
			{
				field: 'clients',
				title: 'Authorized Clients',
				formatter: (cell: CellComponent) =>
					renderWithIcon(
						cell.getRow().getData().everyone ? 'mdi:earth' : 'mdi:ip-network',
						escapeHTML(String(cell.getValue())),
						'text-indigo-500'
					)
			}
		],
		rows: shares.map((share) => {
			const applied = status?.filesystems.find((entry) => entry.guid === share.dataset);
			const dataset = datasets.find((entry) => entry.guid === share.dataset);
			return {
				id: share.id,
				description: share.description || `Share ${share.id}`,
				dataset: applied?.name || share.datasetName || dataset?.name || share.dataset,
				mountpoint: applied?.mountpoint || share.mountpoint || dataset?.mountpoint || '—',
				enabled: share.enabled,
				effective: nfsShareStatus(share, loadFailed ? null : status),
				access: share.readOnly ? 'Read Only' : 'Read / Write',
				security: share.security,
				mapping: share.mapallUser
					? `All → ${share.mapallUser}`
					: share.maprootUser
						? `Root → ${share.maprootUser}`
						: 'Root squash',
				clients: share.everyone ? 'Everyone' : share.clients.join(', '),
				everyone: share.everyone
			};
		})
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button
			size="sm"
			class="h-6"
			{disabled}
			onclick={() => {
				editing = null;
				editorOpen = true;
			}}><span class="icon-[gg--add] mr-1 h-4 w-4"></span>New</Button
		>
		{#if selected}
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				{disabled}
				onclick={() => {
					editing = selected ?? null;
					editorOpen = true;
				}}
				><SpanWithIcon
					icon="icon-[mdi--pencil]"
					size="h-4 w-4"
					gap="gap-2"
					title="Edit Share"
				/></Button
			>
			<Button
				size="sm"
				variant="outline"
				class="h-6.5"
				{disabled}
				onclick={() => (deleteOpen = true)}
				><SpanWithIcon
					icon="icon-[mdi--delete]"
					size="h-4 w-4"
					gap="gap-2"
					title="Delete Share"
				/></Button
			>
		{/if}
	</div>
	{#if loadFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to load NFS shares.
		</p>{/if}
	{#if statusFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to check NFS status.
		</p>{/if}
	{#if !loadFailed && status?.applyStatus === 'pending'}
		<div class="flex items-center gap-2 px-3 py-2 text-xs text-amber-500" role="alert">
			<span class="icon-[mdi--alert-circle-outline] h-4 w-4 shrink-0"></span>
			<p class="min-w-0 flex-1 truncate" title={status.lastError}>
				NFS needs attention{status.lastError ? `: ${status.lastError}` : '.'}
			</p>
			<a class="shrink-0 underline" href={`/${data.node}/storage/nfs/settings`}>Settings</a>
		</div>
	{/if}
	{#if sharesLoading && shares.length === 0}
		<p class="text-muted-foreground px-3 py-2 text-xs">Loading NFS shares...</p>
	{:else}{#key data.node}<TreeTable
				data={table}
				name="nfs-shares-tt"
				bind:parentActiveRow={activeRows}
				multipleSelect={false}
				customPlaceholder="No NFS shares."
				borderTop={loadFailed || statusFailed || status?.applyStatus === 'pending'}
				bind:query
			/>{/key}{/if}
</div>
{#key data.node}
	{#if editorOpen}<Editor
			bind:open={editorOpen}
			node={data.node}
			share={editing}
			bind:datasets
			bind:reload
		/>{/if}
	<AlertDialog
		bind:open={deleteOpen}
		names={{ parent: 'NFS share', element: selected?.description || selected?.dataset || '' }}
		actions={{ onConfirm: remove, onCancel: () => (deleteOpen = false) }}
		loading={removing}
		keepOpenOnConfirm
	/>
{/key}

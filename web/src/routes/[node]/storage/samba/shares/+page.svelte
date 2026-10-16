<script lang="ts">
	import { listGroups } from '$lib/api/auth/groups';
	import { listUsers } from '$lib/api/auth/local';
	import { getSambaConfig } from '$lib/api/samba/config';
	import { deleteSambaShare, getSambaShares } from '$lib/api/samba/share';
	import { getDatasets } from '$lib/api/zfs/datasets';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import Share from '$lib/components/custom/Samba/Share.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Group, User } from '$lib/types/auth';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { SambaConfig } from '$lib/types/samba/config';
	import type { SambaShare } from '$lib/types/samba/shares';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { escapeHTML } from '$lib/utils/string';
	import { convertDbTime } from '$lib/utils/time';
	import { resource, watch } from 'runed';
	import { onMount, untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		node: string;
		shares: SambaShare[];
		datasets: Dataset[];
		groups: Group[];
		users: User[];
		sambaConfig: SambaConfig;
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);

	let datasets = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			const result = await getDatasets(GZFSDatasetTypeSchema.enum.FILESYSTEM, {
				hostname: node,
				signal
			});
			if (signal.aborted || node !== data.node)
				throw new DOMException('Node changed', 'AbortError');
			void updateCache('zfs-filesystems', result, node);
			return result;
		},
		{
			initialValue: initialData.datasets
		}
	);

	let shares = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			const result = await getSambaShares({ hostname: node, signal });
			if (signal.aborted || node !== data.node)
				throw new DOMException('Node changed', 'AbortError');
			void updateCache('samba-shares', result, node);
			return result;
		},
		{
			initialValue: initialData.shares
		}
	);

	const lastGroupsByNode: Record<string, Group[]> = Object.create(null);
	lastGroupsByNode[initialData.node] = initialData.groups;
	let groups = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await listGroups({ hostname: node, signal });
				if (signal.aborted || node !== data.node)
					throw new DOMException('Node changed', 'AbortError');
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastGroupsByNode[node] ?? [];
				}
				lastGroupsByNode[node] = result;
				void updateCache('groups', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) throw new DOMException('Request canceled', 'AbortError');
				throw error;
			}
		},
		{
			initialValue: initialData.groups
		}
	);

	const lastUsersByNode: Record<string, User[]> = Object.create(null);
	lastUsersByNode[initialData.node] = initialData.users;
	let users = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			try {
				const result = await listUsers(undefined, { hostname: node, signal });
				if (signal.aborted || node !== data.node)
					throw new DOMException('Node changed', 'AbortError');
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastUsersByNode[node] ?? [];
				}
				lastUsersByNode[node] = result;
				void updateCache('users', result, node);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) throw new DOMException('Request canceled', 'AbortError');
				throw error;
			}
		},
		{
			initialValue: initialData.users
		}
	);

	onMount(() => {
		for (const loadError of initialData.loadErrors) handleAPIError(loadError);
	});

	let sambaConfig = resource(
		() => data.node,
		async (node, _previousNode, { signal }) => {
			const result = await getSambaConfig({ hostname: node, signal });
			if (signal.aborted || node !== data.node)
				throw new DOMException('Node changed', 'AbortError');
			void updateCache('samba-config', result, node);
			return result;
		},
		{
			initialValue: initialData.sambaConfig
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				datasets.refetch();
				shares.refetch();
				groups.refetch();
				users.refetch();
				reload = false;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));

	let options = {
		create: {
			open: false
		},
		delete: {
			open: false
		},
		edit: {
			open: false,
			share: null as SambaShare | null
		}
	};

	let properties = $state(options);
	let query = $state('');
	watch.pre(
		() => data.node,
		(node, previous) => {
			if (node === previous) return;
			datasets.mutate(data.datasets);
			shares.mutate(data.shares);
			groups.mutate(data.groups);
			users.mutate(data.users);
			sambaConfig.mutate(data.sambaConfig);
			lastGroupsByNode[node] = data.groups;
			lastUsersByNode[node] = data.users;
			activeRows = null;
			properties = {
				create: { open: false },
				delete: { open: false },
				edit: { open: false, share: null }
			};
			reload = false;
			query = '';
			if (previous) for (const loadError of data.loadErrors) handleAPIError(loadError);
		}
	);

	function generateTableData(
		shares: SambaShare[],
		datasets: Dataset[]
	): {
		rows: Row[];
		columns: Column[];
	} {
		function principalFormatter(cell: CellComponent) {
			const principals = cell.getValue() as string[];
			if (!principals?.length) return '-';

			const shown = principals.slice(0, 5).join(', ');
			return escapeHTML(principals.length > 5 ? `${shown}, …` : shown);
		}

		const rows: Row[] = [];
		const columns: Column[] = [
			{
				field: 'id',
				title: 'ID',
				visible: false
			},
			{
				field: 'enabled',
				title: 'Status',
				formatter: (cell: CellComponent) =>
					cell.getValue()
						? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500')
						: renderWithIcon('mdi:close-circle', 'Disabled', 'text-muted-foreground')
			},
			{
				field: 'name',
				title: 'Name'
			},
			{
				field: 'mountpoint',
				title: 'Mount Point'
			},
			{
				field: 'readAccess',
				title: 'Read Access',
				formatter: principalFormatter
			},
			{
				field: 'writeAccess',
				title: 'Write Access',
				formatter: principalFormatter
			},
			{
				field: 'created',
				title: 'Created At',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					return convertDbTime(value);
				}
			}
		];

		for (const share of shares) {
			const dataset = datasets.find((ds) => ds.guid === share.dataset);
			const readAccess = [
				...share.permissions.read.users.map((user) => user.username),
				...share.permissions.read.groups.map((group) => `@${group.name}`),
				...(share.permissions.read.directory ?? []).map(
					(principal) => `${principal.kind === 'group' ? '@' : ''}${principal.name}`
				)
			];
			const writeAccess = [
				...share.permissions.write.users.map((user) => user.username),
				...share.permissions.write.groups.map((group) => `@${group.name}`),
				...(share.permissions.write.directory ?? []).map(
					(principal) => `${principal.kind === 'group' ? '@' : ''}${principal.name}`
				)
			];

			const row: Row = {
				id: share.id,
				name: share.name,
				enabled: share.enabled,
				mountpoint: dataset ? dataset.mountpoint : 'Dataset missing',
				readAccess,
				writeAccess,
				created: share.createdAt
			};

			rows.push(row);
		}

		return {
			rows: rows,
			columns: columns
		};
	}

	let tableData = $derived(generateTableData(shares.current, datasets.current));
</script>

{#snippet button(type: string)}
	{#if activeRows !== null && activeRows.length === 1}
		{#if type === 'edit'}
			<Button
				onclick={() => {
					properties.edit.open = true;
					properties.edit.share =
						shares.current.find((share) => share.id === Number(activeRow?.id)) || null;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit Share" />
			</Button>
		{/if}

		{#if type === 'delete'}
			<Button
				onclick={() => {
					properties.delete.open = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5"
			>
				<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete Share" />
			</Button>
		{/if}
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button
			onclick={() => {
				properties.create.open = true;
			}}
			size="sm"
			class="h-6"
		>
			<div class="flex items-center">
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>

				<span>New</span>
			</div>
		</Button>

		{@render button('edit')}
		{@render button('delete')}
	</div>

	{#key data.node}<TreeTable
			data={tableData}
			name="samba-shares-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={true}
			bind:query
		/>{/key}
</div>

{#if properties.create.open}
	<Share
		node={data.node}
		bind:open={properties.create.open}
		shares={shares.current}
		datasets={datasets.current}
		groups={groups.current}
		users={users.current}
		appleExtensions={sambaConfig.current.appleExtensions}
		bind:reload
	/>
{/if}

{#if properties.edit.open}
	<Share
		node={data.node}
		bind:open={properties.edit.open}
		shares={shares.current}
		datasets={datasets.current}
		groups={groups.current}
		users={users.current}
		share={properties.edit.share}
		edit={properties.edit.open}
		appleExtensions={sambaConfig.current.appleExtensions}
		bind:reload
	/>
{/if}

<AlertDialog
	open={properties.delete.open}
	names={{ parent: 'Samba share', element: activeRow ? (activeRow.name as string) : '' }}
	actions={{
		onConfirm: async () => {
			if (activeRow) {
				const node = data.node;
				const response = await deleteSambaShare(Number(activeRow.id), { hostname: node });
				if (node !== data.node) return;
				if (response.status === 'error') {
					handleAPIError(response);
					toast.error('Failed to delete Samba share', {
						position: 'bottom-center'
					});

					return;
				}

				toast.success('Samba share deleted', {
					position: 'bottom-center'
				});

				properties.delete.open = false;
				activeRows = null;
				reload = true;
			}
		},
		onCancel: () => {
			properties.delete.open = false;
		}
	}}
></AlertDialog>

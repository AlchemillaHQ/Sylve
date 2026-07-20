<script lang="ts">
	import {
		deleteDynamicDNSEntry,
		getDynamicDNSEntries,
		syncDynamicDNSEntry
	} from '$lib/api/services/dynamic-dns';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Form from '$lib/components/custom/Services/DynamicDNS/Form.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSwitches } from '$lib/api/network/switch';
	import type { Iface } from '$lib/types/network/iface';
	import type { SwitchList } from '$lib/types/network/switch';
	import type { DynamicDNSEntry } from '$lib/types/services/dynamic-dns';
	import { convertDbTime } from '$lib/utils/time';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource } from 'runed';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		entries: DynamicDNSEntry[];
		interfaces: Iface[];
		switches?: SwitchList;
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	const entriesResource = resource(
		() => 'dynamic-dns-entries',
		async (key) => {
			const result = await getDynamicDNSEntries();
			if (Array.isArray(result)) {
				updateCache(key, result);
				return result;
			}
			return [] as DynamicDNSEntry[];
		},
		{ initialValue: data.entries }
	);

	const entries = $derived(
		Array.isArray(entriesResource.current) ? (entriesResource.current as DynamicDNSEntry[]) : []
	);

	// svelte-ignore state_referenced_locally
	const interfacesResource = resource(
		() => 'network-ifaces',
		async (key) => {
			const result = await getInterfaces();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.interfaces }
	);
	const interfaces = $derived(
		Array.isArray(interfacesResource.current) ? (interfacesResource.current as Iface[]) : []
	);

	// svelte-ignore state_referenced_locally
	const switchesResource = resource(
		() => 'network-switches',
		async (key) => {
			const result = await getSwitches();
			updateCache(key, result);
			return result;
		},
		{ initialValue: data.switches ?? { standard: [], manual: [] } }
	);
	const switches = $derived(
		switchesResource.current &&
			typeof switchesResource.current === 'object' &&
			!Array.isArray(switchesResource.current) &&
			'status' in switchesResource.current
			? { standard: [], manual: [] }
			: ((switchesResource.current as SwitchList) ?? { standard: [], manual: [] })
	);

	let activeRow = $state<Row[] | null>(null);
	let query = $state('');
	let syncing = $state(false);
	let modals = $state({
		create: { open: false },
		edit: { open: false, id: 0 },
		delete: { open: false }
	});
	let errorModal = $state({
		hostname: '',
		open: false,
		error: ''
	});
	const htmlEscapes: Record<string, string> = {
		'&': '&amp;',
		'<': '&lt;',
		'>': '&gt;',
		"'": '&#39;',
		'"': '&quot;'
	};

	const selectedEntry = $derived.by(() => {
		if (!activeRow || activeRow.length !== 1) return null;
		return entries.find((entry) => entry.id === Number(activeRow[0]?.id)) ?? null;
	});

	function escapeHTML(value: unknown): string {
		return String(value ?? '').replace(/[&<>'"]/g, (character) => {
			return htmlEscapes[character] ?? character;
		});
	}

	function sourceValue(entry: DynamicDNSEntry): string {
		if (entry.sourceType === 'interface') return entry.sourceSettings.interface || 'Interface';
		if (entry.sourceType === 'manual') return 'Manual';
		return entry.sourceSettings.server || 'STUN';
	}

	function formatEnabled(value: boolean): string {
		return value
			? renderWithIcon('mdi:check-circle', 'Enabled', 'text-green-500', 'Enabled')
			: renderWithIcon('mdi:close-circle', 'Disabled', 'text-red-400', 'Disabled');
	}

	function recordTypeBadge(value: string): string {
		const labels = value === 'BOTH' ? ['A', 'AAAA'] : [value];
		return labels
			.map((label) => {
				const color =
					label === 'A'
						? 'text-blue-400 border-blue-400/50'
						: 'text-violet-400 border-violet-400/50';
				return `<span class="inline-flex items-center rounded border px-1 text-xs font-mono leading-tight ${color}">${label}</span>`;
			})
			.join('<span class="mx-1 text-muted-foreground/50">+</span>');
	}

	function formatSource(cell: CellComponent): string {
		const row = cell.getRow().getData();
		const icon =
			row.sourceType === 'interface'
				? 'mdi:ethernet'
				: row.sourceType === 'manual'
					? 'mdi:ip'
					: 'mdi:radar';
		const color =
			row.sourceType === 'interface'
				? 'text-cyan-400'
				: row.sourceType === 'manual'
					? 'text-indigo-400'
					: 'text-amber-400';
		return renderWithIcon(icon, escapeHTML(cell.getValue()), color);
	}

	function formatProvider(cell: CellComponent): string {
		if (cell.getValue() === 'namecheap') {
			return renderWithIcon('simple-icons:namecheap', 'Namecheap', 'text-orange-500');
		}
		return renderWithIcon('simple-icons:cloudflare', 'Cloudflare', 'text-orange-400');
	}

	function formatStatus(cell: CellComponent): string {
		const row = cell.getRow().getData() as { enabled: boolean };
		const icons = [formatEnabled(row.enabled)];

		switch (cell.getValue()) {
			case 'success':
				icons.push(renderWithIcon('mdi:check-circle-outline', 'Up to date', 'text-green-500'));
				break;
			case 'partial':
				icons.push(renderWithIcon('mdi:alert-circle-outline', 'Partial', 'text-amber-400'));
				break;
			case 'error':
				icons.push(renderWithIcon('mdi:close-circle-outline', 'Error', 'text-red-400'));
				break;
			default:
				icons.push(renderWithIcon('mdi:clock-outline', 'Pending', 'text-muted-foreground'));
		}

		return `<div class="flex flex-col gap-1">${icons.join(' ')}</div>`;
	}

	function openErrorModal(hostname: string, error: string) {
		errorModal.hostname = hostname;
		errorModal.error = error;
		errorModal.open = true;
	}

	async function copyErrorFromModal() {
		if (!errorModal.error) return;
		try {
			await navigator.clipboard.writeText(errorModal.error);
			toast.success('Error copied to clipboard', { position: 'bottom-center' });
		} catch {
			toast.error('Failed to copy error', { position: 'bottom-center' });
		}
	}

	function refetchEntries() {
		void entriesResource.refetch();
	}

	function refreshData() {
		void entriesResource.refetch();
		void interfacesResource.refetch();
		void switchesResource.refetch();
	}

	async function syncSelectedEntry() {
		const entry = selectedEntry;
		if (!entry) return;

		syncing = true;
		const result = await syncDynamicDNSEntry(entry.id);
		syncing = false;
		if (isAPIResponse(result)) {
			handleAPIError(result);
			toast.error(`Failed to sync ${entry.hostname}`, { position: 'bottom-center' });
			return;
		}

		await entriesResource.refetch();
		if (result.lastStatus === 'success') {
			toast.success(`${result.hostname} is up to date`, { position: 'bottom-center' });
		} else if (result.lastStatus === 'partial') {
			toast.warning(`${result.hostname} updated one address family`, { position: 'bottom-center' });
		} else {
			toast.error(`Sync for ${result.hostname} finished with errors`, {
				position: 'bottom-center'
			});
		}
	}

	async function confirmDelete() {
		const entry = selectedEntry;
		if (!entry) return;

		const result = await deleteDynamicDNSEntry(entry.id);
		if (result.status === 'error') {
			handleAPIError(result);
			toast.error(`Failed to delete ${entry.hostname}`, { position: 'bottom-center' });
			return;
		}

		toast.success('Dynamic DNS entry deleted', { position: 'bottom-center' });
		activeRow = null;
		modals.delete.open = false;
		await entriesResource.refetch();
	}

	const columns: Column[] = $derived([
		{ field: 'id', title: 'ID', visible: false },
		{ field: 'sourceType', title: 'sourceType', visible: false },
		{
			field: 'lastStatus',
			title: 'Status',
			formatter: formatStatus
		},
		{
			field: 'recordType',
			title: 'Type',
			formatter: (cell: CellComponent) => recordTypeBadge(String(cell.getValue()))
		},
		{
			field: 'provider',
			title: 'Provider',
			formatter: formatProvider
		},
		{
			field: 'hostname',
			title: 'Hostname',
			formatter: (cell: CellComponent) =>
				renderWithIcon('mdi:dns', escapeHTML(cell.getValue()), 'text-indigo-400'),
			copyOnClick: true
		},
		{
			field: 'source',
			title: 'Source',
			formatter: formatSource
		},
		{
			field: 'lastIPv4',
			title: 'IPv4',
			formatter: (cell: CellComponent) => escapeHTML(cell.getValue() || '-'),
			copyOnClick: true
		},
		{
			field: 'lastIPv6',
			title: 'IPv6',
			formatter: (cell: CellComponent) => escapeHTML(cell.getValue() || '-'),
			copyOnClick: true
		},
		{
			field: 'lastSyncAt',
			title: 'Last Sync',
			formatter: (cell: CellComponent) => (cell.getValue() ? convertDbTime(cell.getValue()) : '-')
		},
		{
			field: 'lastError',
			title: 'Last Error',
			cellClick: (_event: UIEvent, cell: CellComponent) => {
				const error = String(cell.getValue() || '');
				if (!error) return;
				openErrorModal(String(cell.getRow().getData().hostname || ''), error);
			},
			formatter: (cell: CellComponent) => {
				const value = String(cell.getValue() || '');
				return value
					? renderWithIcon('mdi:alert-outline', escapeHTML(value), 'cursor-pointer text-red-400')
					: '-';
			}
		}
	]);

	const tableData = $derived({
		columns,
		rows: entries.map((entry) => ({
			id: entry.id,
			enabled: entry.enabled,
			hostname: entry.hostname,
			provider: entry.provider,
			recordType: entry.recordType,
			sourceType: entry.sourceType,
			source: sourceValue(entry),
			lastIPv4: entry.lastIPv4,
			lastIPv6: entry.lastIPv6,
			lastStatus: entry.lastStatus,
			lastSyncAt: entry.lastSyncAt,
			lastError: entry.lastError
		}))
	});
</script>

{#snippet selectedActions()}
	{#if selectedEntry}
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			onclick={syncSelectedEntry}
			disabled={syncing}
		>
			<SpanWithIcon icon="icon-[mdi--sync]" size="h-4 w-4" gap="gap-2" title="Sync" />
		</Button>
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			onclick={() => {
				modals.edit.id = selectedEntry.id;
				modals.edit.open = true;
			}}
		>
			<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-2" title="Edit" />
		</Button>
		<Button size="sm" variant="outline" class="h-6.5" onclick={() => (modals.delete.open = true)}>
			<SpanWithIcon icon="icon-[mdi--delete]" size="h-4 w-4" gap="gap-2" title="Delete" />
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" onclick={() => (modals.create.open = true)}>
			<SpanWithIcon icon="icon-[gg--add]" size="h-4 w-4" gap="gap-2" title="New" />
		</Button>
		{@render selectedActions()}
		<Button size="sm" variant="outline" class="ml-auto h-6" title="Refresh" onclick={refreshData}>
			<span class="icon-[mdi--refresh] h-4 w-4"></span>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={tableData}
			name="tt-dynamic-dns"
			bind:parentActiveRow={activeRow}
			bind:query
			multipleSelect={false}
			dataTree={false}
		/>
	</div>
</div>

{#if modals.create.open}
	<Form
		bind:open={modals.create.open}
		{entries}
		{interfaces}
		{switches}
		edit={false}
		afterChange={refetchEntries}
	/>
{/if}

{#if modals.edit.open}
	<Form
		bind:open={modals.edit.open}
		{entries}
		{interfaces}
		{switches}
		edit={true}
		id={modals.edit.id}
		afterChange={refetchEntries}
	/>
{/if}

<AlertDialog
	open={modals.delete.open}
	names={{ parent: 'Dynamic DNS entry', element: selectedEntry?.hostname ?? '' }}
	actions={{
		onConfirm: () => void confirmDelete(),
		onCancel: () => {
			modals.delete.open = false;
		}
	}}
/>

<Dialog.Root bind:open={errorModal.open}>
	<Dialog.Content class="p-5" showCloseButton={true}>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[mdi--alert-circle-outline]"
					size="h-5 w-5"
					title={`${errorModal.hostname || 'Dynamic DNS'} - Error Details`}
					gap="gap-2 mt-1"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-3">
			<pre class="m-0 whitespace-pre-wrap wrap-break-word text-sm">{errorModal.error || '-'}</pre>
		</div>

		<Dialog.Footer>
			<Button onclick={copyErrorFromModal}>Copy</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

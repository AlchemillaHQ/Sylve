<script lang="ts">
	import { storage } from '$lib';
	import { applyNFS, getNFSConfig, getNFSPreview, getNFSStatus } from '$lib/api/nfs';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import Editor from '$lib/components/custom/NFS/Config.svelte';
	import Preview from '$lib/components/custom/NFS/Preview.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import { NFSMutationSchema, type NFSPreview, type NFSStatus } from '$lib/types/nfs';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { escapeHTML } from '$lib/utils/string';
	import { renderWithIcon } from '$lib/utils/table';
	import { convertDbTime } from '$lib/utils/time';
	import { useInterval, watch } from 'runed';
	import { onDestroy, onMount, untrack } from 'svelte';
	import type { PageData } from './$types';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	let { data }: { data: PageData } = $props();
	const initial = untrack(() => data);
	let config = $state(initial.config);
	let status = $state<NFSStatus | null>(null);
	let loadFailed = $state(false);
	let statusFailed = $state(false);
	let configVersion: string | null = null;
	let query = $state('');
	let editorOpen = $state(false);
	let previewOpen = $state(false);
	let preview = $state<NFSPreview | null>(null);
	let previewing = $state(false);
	let refreshing = $state(false);
	let polling = $state(false);
	let applyOpen = $state(false);
	let runtimeBusy = $state(false);
	let reload = $state(false);
	let generation = 0;
	let reads: AbortController | null = null;
	let previewRead: AbortController | null = null;
	onDestroy(() => {
		generation++;
		reads?.abort();
		previewRead?.abort();
	});
	onMount(() => {
		void refresh(true);
	});
	let disabled = $derived(loadFailed || refreshing || previewing || runtimeBusy);
	watch.pre(
		() => data,
		(next) => {
			generation++;
			reads?.abort();
			previewRead?.abort();
			config = next.config;
			status = null;
			loadFailed = false;
			statusFailed = false;
			configVersion = null;
			editorOpen = false;
			previewOpen = false;
			preview = null;
			previewing = false;
			refreshing = false;
			polling = false;
			reload = false;
			query = '';
			applyOpen = false;
			runtimeBusy = false;
			void refresh(true);
		}
	);
	async function refresh(background = false) {
		if (
			refreshing ||
			polling ||
			previewing ||
			(background && (runtimeBusy || reload || editorOpen || previewOpen || applyOpen))
		)
			return;
		if (background) polling = true;
		else refreshing = true;
		const node = data.node;
		const currentGeneration = generation;
		reads = new AbortController();
		const canApply = () =>
			generation === currentGeneration &&
			(!background ||
				(!runtimeBusy && !reload && !editorOpen && !previewOpen && !previewing && !applyOpen));
		try {
			const nextStatus = await getNFSStatus({ hostname: node, signal: reads.signal });
			if (!canApply()) return;
			status = nextStatus;
			statusFailed = false;
			if (!background || loadFailed || configVersion !== nextStatus.configVersion) {
				try {
					const result = await getNFSConfig({ hostname: node, signal: reads.signal });
					if (!canApply()) return;
					config = result;
					configVersion = nextStatus.configVersion;
					loadFailed = false;
					void updateCache('nfs-config', result, node);
				} catch {
					if (canApply()) loadFailed = true;
				}
			}
		} catch {
			if (canApply()) {
				status = null;
				statusFailed = true;
			}
		} finally {
			if (generation === currentGeneration) {
				if (background) polling = false;
				else refreshing = false;
			}
		}
	}
	async function showPreview() {
		if (disabled) return;
		previewing = true;
		const node = data.node;
		const currentGeneration = generation;
		previewRead = new AbortController();
		try {
			const result = await getNFSPreview({ hostname: node, signal: previewRead.signal });
			if (generation !== currentGeneration) return;
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return;
			}
			preview = result;
			previewOpen = true;
		} finally {
			if (generation === currentGeneration) previewing = false;
		}
	}
	async function changeRuntime() {
		if (!applyOpen || disabled) return;
		const node = data.node;
		const currentGeneration = generation;
		runtimeBusy = true;
		try {
			const result = await applyNFS({ hostname: node });
			if (generation !== currentGeneration) return;
			if (result.status === 'error') {
				handleAPIError(result);
				return;
			}
			const mutation = NFSMutationSchema.safeParse(result.data);
			if (mutation.success && mutation.data.applyStatus === 'stopped') {
				toast.success('NFS disabled', { position: 'bottom-center' });
			} else if (mutation.success && mutation.data.applied) {
				toast.success('NFS settings applied', { position: 'bottom-center' });
			} else {
				toast.warning('NFS apply pending', {
					position: 'bottom-center'
				});
			}
			applyOpen = false;
			reload = true;
		} finally {
			if (generation === currentGeneration) runtimeBusy = false;
		}
	}
	watch(
		[() => reload, () => refreshing, () => polling, () => runtimeBusy, () => previewing],
		([requested, refreshing, polling, runtimeBusy, previewing]) => {
			if (requested && !refreshing && !polling && !runtimeBusy && !previewing) {
				reload = false;
				preview = null;
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
	const statusStyles: Record<string, { icon: string; color: string }> = {
		Enabled: { icon: 'mdi:check-circle', color: 'text-green-500' },
		Disabled: { icon: 'mdi:close-circle', color: 'text-muted-foreground' },
		Running: { icon: 'mdi:play-circle-outline', color: 'text-green-500' },
		Stopped: { icon: 'mdi:stop', color: 'text-muted-foreground' },
		'Not managed': { icon: 'mdi:pause-circle-outline', color: 'text-muted-foreground' },
		Unknown: { icon: 'mdi:help-circle-outline', color: 'text-muted-foreground' }
	};
	const applyStyles: Record<NFSStatus['applyStatus'], { label: string; color: string }> = {
		pending: { label: 'Pending', color: 'text-amber-500 border-amber-500/50' },
		applying: { label: 'Applying', color: 'text-sky-500 border-sky-500/50' },
		starting: { label: 'Starting', color: 'text-sky-500 border-sky-500/50' },
		applied: { label: 'Applied', color: 'text-green-500 border-green-500/50' },
		idle: { label: 'Idle', color: 'text-muted-foreground border-muted-foreground/50' },
		stopped: { label: 'Stopped', color: 'text-muted-foreground border-muted-foreground/50' }
	};
	function badge(label: string, color: string) {
		return `<span class="inline-flex items-center rounded border px-1 text-xs font-mono leading-tight ${color}">${escapeHTML(label)}</span>`;
	}
	function formatValue(cell: CellComponent) {
		const row = cell.getRow().getData();
		const value = String(cell.getValue());
		const label = escapeHTML(value);
		switch (row.id) {
			case 'enabled':
			case 'runtime':
			case 'numeric-ids':
			case 'udp': {
				const style = statusStyles[value];
				return style ? renderWithIcon(style.icon, label, style.color) : label;
			}
			case 'applied': {
				const style = applyStyles[value as NFSStatus['applyStatus']];
				return style
					? badge(style.label, style.color)
					: renderWithIcon('mdi:help-circle-outline', label, 'text-muted-foreground');
			}
			case 'exports':
			case 'effective':
				return typeof cell.getValue() === 'number'
					? badge(
							value,
							cell.getValue() === 0
								? 'text-muted-foreground border-muted-foreground/50'
								: 'text-sky-500 border-sky-500/50'
						)
					: renderWithIcon('mdi:help-circle-outline', label, 'text-muted-foreground');
			case 'protocol':
				return `<span class="inline-flex items-center gap-1">${value
					.split(' and ')
					.map((protocol) =>
						badge(
							protocol,
							protocol === 'NFSv3'
								? 'text-sky-500 border-sky-500/50'
								: 'text-violet-500 border-violet-500/50'
						)
					)
					.join('')}</span>`;
			case 'v4-max-version':
				return badge(value, 'text-violet-500 border-violet-500/50');
			case 'ports':
				return renderWithIcon(
					value === 'Required' ? 'mdi:shield-check-outline' : 'mdi:shield-alert-outline',
					label,
					value === 'Required' ? 'text-green-500' : 'text-amber-500'
				);
			case 'bindings':
				return value === 'All Interfaces'
					? renderWithIcon('mdi:earth', label, 'text-sky-500')
					: `<span class="inline-flex flex-wrap items-center gap-1">${value
							.split(', ')
							.map((address) => badge(address, 'text-sky-500 border-sky-500/50'))
							.join('')}</span>`;
			case 'mountd-port':
			case 'threads':
				return value === 'Automatic'
					? `<span class="text-muted-foreground">${label}</span>`
					: badge(value, 'text-muted-foreground border-muted-foreground/50');
			case 'kerberos-principal':
				return value === 'None'
					? `<span class="text-muted-foreground">${label}</span>`
					: renderWithIcon(
							'mdi:key-chain',
							`<span class="font-mono text-xs">${label}</span>`,
							'text-violet-500'
						);
			case 'error':
				return row.hasError
					? renderWithIcon('mdi:alert-circle', label, 'text-red-500')
					: `<span class="text-muted-foreground">${label}</span>`;
			case 'checked':
				return `<span class="font-mono text-xs text-muted-foreground">${label}</span>`;
			default:
				return label;
		}
	}
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property', formatter: 'plaintext' },
			{ title: 'Value', field: 'value', formatter: formatValue }
		],
		rows: config
			? [
					{
						id: 'enabled',
						property: 'Enabled',
						value: status ? (status.enabled ? 'Enabled' : 'Disabled') : 'Unknown'
					},
					{
						id: 'runtime',
						property: 'Status',
						value: !status
							? 'Unknown'
							: status.running === null
								? status.enabled
									? 'Unknown'
									: 'Not managed'
								: status.running
									? 'Running'
									: 'Stopped'
					},
					{ id: 'exports', property: 'Enabled Shares', value: status?.desiredExports ?? 'Unknown' },
					{ id: 'applied', property: 'Apply Status', value: status?.applyStatus ?? 'Unknown' },
					{
						id: 'checked',
						property: 'Last Check',
						value: status?.checkedAt
							? convertDbTime(status.checkedAt, 'yyyy-MM-dd HH:mm:ss')
							: 'Unknown'
					},
					{
						id: 'effective',
						property: 'Exported Filesystems',
						value: status?.appliedExports ?? 'Unknown'
					},
					{
						id: 'error',
						property: 'Last Apply Error',
						value: status ? status.lastError || 'None' : 'Unknown',
						hasError: Boolean(status?.lastError)
					},
					{
						id: 'protocol',
						property: 'Protocol',
						value:
							config.protocol === 'v3v4'
								? 'NFSv3 and NFSv4'
								: config.protocol === 'v4'
									? 'NFSv4'
									: 'NFSv3'
					},
					{
						id: 'v4-max-version',
						property: 'Maximum NFSv4 Version',
						value: `NFSv${config.v4MaxVersion}`
					},
					{
						id: 'numeric-ids',
						property: 'Numeric IDs (NFSv4)',
						value: config.numericIds ? 'Enabled' : 'Disabled'
					},
					{ id: 'udp', property: 'UDP (NFSv3)', value: config.udp ? 'Enabled' : 'Disabled' },
					{
						id: 'ports',
						property: 'Privileged Ports',
						value: config.allowNonroot ? 'Optional' : 'Required'
					},
					{
						id: 'bindings',
						property: 'Bind Addresses',
						value: config.bindAddresses.join(', ') || 'All Interfaces'
					},
					{ id: 'mountd-port', property: 'Mountd Port', value: config.mountdPort || 'Automatic' },
					{
						id: 'threads',
						property: 'Threads',
						value: config.minThreads ? `${config.minThreads} – ${config.maxThreads}` : 'Automatic'
					},
					{
						id: 'kerberos-principal',
						property: 'Kerberos Principal',
						value: config.kerberosPrincipal || 'None'
					}
				]
			: []
	});
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />
		<Button size="sm" class="h-6" disabled={disabled || !config} onclick={() => (editorOpen = true)}
			><SpanWithIcon
				icon="icon-[hugeicons--system-update-01]"
				size="h-4 w-4"
				gap="gap-2"
				title="Update"
			/></Button
		>
		<Button
			size="sm"
			variant="outline"
			class="h-6.5"
			disabled={disabled || !status?.runtimeAvailable || (!status.enabled && !status.managed)}
			onclick={() => (applyOpen = true)}
			><SpanWithIcon
				icon="icon-[mdi--reload]"
				size="h-4 w-4"
				gap="gap-2"
				title="Re-Apply / Retry"
			/></Button
		>
		<Button size="sm" variant="outline" class="h-6.5" {disabled} onclick={showPreview}
			><SpanWithIcon
				icon="icon-[mdi--file-eye-outline]"
				size="h-4 w-4"
				gap="gap-2"
				title={previewing ? 'Validating...' : 'Preview Exports'}
			/></Button
		>
	</div>
	{#if loadFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to load NFS settings.
		</p>{/if}
	{#if statusFailed}<p class="text-destructive px-3 py-2 text-xs" role="alert">
			Unable to check NFS status.
		</p>{/if}
	{#key data.node}<TreeTable
			data={table}
			name="nfs-config-tt"
			multipleSelect={false}
			borderTop={loadFailed || statusFailed}
			bind:query
		/>{/key}
</div>
{#key data.node}
	{#if config && editorOpen}<Editor
			bind:open={editorOpen}
			node={data.node}
			{config}
			bind:reload
		/>{/if}
	{#if preview && previewOpen}<Preview bind:open={previewOpen} {preview} />{/if}
	<AlertDialog
		bind:open={applyOpen}
		title="Re-apply NFS?"
		description="Re-applying NFS settings may interrupt connected clients."
		actions={{ onConfirm: changeRuntime, onCancel: () => (applyOpen = false) }}
		loading={runtimeBusy}
		keepOpenOnConfirm
	/>
{/key}

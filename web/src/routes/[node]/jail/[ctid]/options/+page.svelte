<script lang="ts">
	import { getJailByCTID } from '$lib/api/jail/jail';
	import AllowedOptions from '$lib/components/custom/Jail/Options/AllowedOptions.svelte';
	import LifecycleHooks from '$lib/components/custom/Jail/Options/LifecycleHooks.svelte';
	import StartOrder from '$lib/components/custom/Jail/Options/StartOrder.svelte';
	import TextEdit from '$lib/components/custom/Jail/Options/TextEdit.svelte';
	import WoL from '$lib/components/custom/Jail/Options/WoL.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import { jailPowerSignal } from '$lib/stores/api.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import type { Dataset } from '$lib/types/zfs/dataset';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { isBoolean } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import { getContext, onMount } from 'svelte';
	import type { CellComponent } from 'tabulator-tables';

	type OptionDialog =
		| 'startOrder'
		| 'wol'
		| 'fstab'
		| 'resolvConf'
		| 'devfsRules'
		| 'additionalOptions'
		| 'allowedOptions'
		| 'metadata'
		| 'lifecycleHooks';

	interface Data {
		node: string;
		ctId: number;
		jail: Jail | null;
		jailError: APIResponse | null;
		devFSDisabled: boolean;
		basicInfoError: APIResponse | null;
		filesystems: Dataset[];
		filesystemsError: APIResponse | null;
	}

	interface JailResourceValue {
		identity: string;
		value: Jail | null;
	}

	let { data }: { data: Data } = $props();
	const initialIdentity = () => `${data.node}:${data.ctId}`;
	const initialJail = () => data.jail;
	let lastJailIdentity = initialIdentity();
	let lastJail = initialJail();

	const jailResource = resource(
		[() => data.node, () => data.ctId],
		async ([hostname, ctId], _, { signal }): Promise<JailResourceValue> => {
			const identity = `${hostname}:${ctId}`;
			if (lastJailIdentity !== identity) {
				lastJailIdentity = identity;
				lastJail = identity === initialIdentity() ? data.jail : null;
			}
			const result = await getJailByCTID(ctId, {
				hostname,
				signal
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return { identity, value: lastJail };
			}
			lastJail = result;
			await updateCache(`jail-${ctId}`, result, hostname);
			return { identity, value: result };
		},
		{ initialValue: { identity: initialIdentity(), value: initialJail() } }
	);

	const jailState = getContext<{ current: JailState | null; refetch(): void }>('jailState');
	let currentJail = $derived(
		jailResource.current.identity === initialIdentity() ? jailResource.current.value : null
	);
	let canMutate = $derived(
		!!currentJail &&
			jailState.current?.ctId === data.ctId &&
			(jailState.current.state === 'ACTIVE' || jailState.current.state === 'INACTIVE') &&
			!jailState.current.pendingAction
	);
	let devFSDisabled = $derived(data.devFSDisabled ?? false);
	let jailRoot = $derived.by(() => {
		if (!currentJail) return null;
		const baseStorage = currentJail.storages.find((storage) => storage.isBase);
		if (!baseStorage) return null;

		const rootDataset = data.filesystems.find((dataset) => dataset.guid === baseStorage.guid);
		const mountpoint = rootDataset?.mountpoint.replace(/\/+$/, '') ?? '';
		return mountpoint.startsWith('/') ? mountpoint : null;
	});

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (isBoolean(value)) {
						if (value === true || value === 'true') return 'Yes';
						if (value === false || value === 'false') return 'No';
					}
					return value;
				}
			}
		] as Column[],
		rows: currentJail
			? [
					{
						id: 'startOrder',
						property: 'Start At Boot / Start Order',
						value: `${currentJail.startAtBoot ? 'Yes' : 'No'} / ${currentJail.startOrder ?? 0}`
					},
					{ id: 'wol', property: 'Wake on LAN', value: currentJail.wol ?? false },
					{
						id: 'fstab',
						property: 'FSTab Entries',
						value: preview(currentJail.fstab)
					},
					{
						id: 'resolvConf',
						property: '/etc/resolv.conf',
						value: preview(currentJail.resolvConf)
					},
					...(devFSDisabled
						? []
						: [
								{
									id: 'devfsRules',
									property: 'DevFS Ruleset',
									value: preview(currentJail.devfsRuleset)
								}
							]),
					{
						id: 'additionalOptions',
						property: 'Additional Options',
						value: preview(currentJail.additionalOptions)
					},
					{
						id: 'allowedOptions',
						property: 'Allowed Options',
						value: listPreview(currentJail.allowedOptions)
					},
					{
						id: 'metadata',
						property: 'Metadata',
						value: metadataPreview(currentJail)
					},
					{
						id: 'lifecycleHooks',
						property: 'Lifecycle Hooks',
						value: listPreview(
							(currentJail.jailHooks || [])
								.filter((hook) => hook.enabled && hook.script?.trim())
								.map((hook) => hook.phase)
						)
					}
				]
			: []
	});

	let activeRows = $state<Row[] | null>(null);
	let activeRow = $derived<Row | null>(activeRows?.[0] ?? null);
	let query = $state('');
	let properties = $state<Record<OptionDialog, { open: boolean }>>({
		startOrder: { open: false },
		wol: { open: false },
		fstab: { open: false },
		resolvConf: { open: false },
		devfsRules: { open: false },
		additionalOptions: { open: false },
		allowedOptions: { open: false },
		metadata: { open: false },
		lifecycleHooks: { open: false }
	});

	function preview(value?: string): string {
		if (!value) return '—';
		return value.split('\n')[0] + (value.includes('\n') ? '…' : '');
	}

	function listPreview(values: string[]): string {
		if (values.length === 0) return '—';
		if (values.length === 1) return values[0];
		return `${values[0]} (+${values.length - 1} more)`;
	}

	function metadataPreview(jail: Jail): string {
		if (!jail.metadataMeta && !jail.metadataEnv) return '—';
		return [
			jail.metadataMeta && `meta: ${preview(jail.metadataMeta)}`,
			jail.metadataEnv && `env: ${preview(jail.metadataEnv)}`
		]
			.filter(Boolean)
			.join(' | ');
	}

	onMount(() => {
		if (data.jailError) handleAPIError(data.jailError);
		if (data.basicInfoError) handleAPIError(data.basicInfoError);
		if (data.filesystemsError) handleAPIError(data.filesystemsError);
	});

	async function reloadJail() {
		await jailResource.refetch();
		jailState.refetch();
	}

	watch(
		() => jailPowerSignal.token,
		() => {
			void reloadJail();
		}
	);
</script>

{#snippet editButton(type: OptionDialog, title: string)}
	<Button
		onclick={() => {
			properties[type].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5 disabled:pointer-events-auto!"
		disabled={!canMutate}
		title={!canMutate ? 'Wait for the current jail operation to finish' : ''}
	>
		<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit {title}" />
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRow}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if activeRow.id === 'startOrder'}
				{@render editButton('startOrder', 'Start At Boot / Start Order')}
			{:else if activeRow.id === 'wol'}
				{@render editButton('wol', 'Wake on LAN')}
			{:else if activeRow.id === 'fstab'}
				{@render editButton('fstab', 'FSTab Entries')}
			{:else if activeRow.id === 'resolvConf'}
				{@render editButton('resolvConf', '/etc/resolv.conf')}
			{:else if activeRow.id === 'devfsRules'}
				{@render editButton('devfsRules', 'DevFS Ruleset')}
			{:else if activeRow.id === 'additionalOptions'}
				{@render editButton('additionalOptions', 'Additional Options')}
			{:else if activeRow.id === 'allowedOptions'}
				{@render editButton('allowedOptions', 'Allowed Options')}
			{:else if activeRow.id === 'metadata'}
				{@render editButton('metadata', 'Metadata')}
			{:else if activeRow.id === 'lifecycleHooks'}
				{@render editButton('lifecycleHooks', 'Lifecycle Hooks')}
			{/if}
		</div>
	{/if}

	{#if !currentJail}
		<div class="m-3 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm">
			Unable to load this jail's options.
		</div>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="jail-options-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.startOrder.open && currentJail}
	<StartOrder
		bind:open={properties.startOrder.open}
		jail={currentJail}
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.wol.open && currentJail}
	<WoL bind:open={properties.wol.open} jail={currentJail} node={data.node} onSaved={reloadJail} />
{/if}

{#if properties.fstab.open && currentJail}
	<TextEdit
		bind:open={properties.fstab.open}
		jail={currentJail}
		type="fstab"
		node={data.node}
		{jailRoot}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.resolvConf.open && currentJail}
	<TextEdit
		bind:open={properties.resolvConf.open}
		jail={currentJail}
		type="resolvConf"
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.devfsRules.open && currentJail}
	<TextEdit
		bind:open={properties.devfsRules.open}
		jail={currentJail}
		type="devfsRules"
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.additionalOptions.open && currentJail}
	<TextEdit
		bind:open={properties.additionalOptions.open}
		jail={currentJail}
		type="additionalOptions"
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.allowedOptions.open && currentJail}
	<AllowedOptions
		bind:open={properties.allowedOptions.open}
		jail={currentJail}
		node={data.node}
		onSaved={reloadJail}
		{devFSDisabled}
	/>
{/if}

{#if properties.metadata.open && currentJail}
	<TextEdit
		bind:open={properties.metadata.open}
		jail={currentJail}
		type="metadata"
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if properties.lifecycleHooks.open && currentJail}
	<LifecycleHooks
		bind:open={properties.lifecycleHooks.open}
		jail={currentJail}
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

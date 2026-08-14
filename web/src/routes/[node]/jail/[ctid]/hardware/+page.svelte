<script lang="ts">
	import { getJailByCTID } from '$lib/api/jail/jail';
	import { updateResourceLimits } from '$lib/api/jail/hardware';
	import AlertDialog from '$lib/components/custom/Dialog/Alert.svelte';
	import CPU from '$lib/components/custom/Jail/Hardware/CPU.svelte';
	import RAM from '$lib/components/custom/Jail/Hardware/RAM.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import { jailPowerSignal } from '$lib/stores/api.svelte';
	import type { APIResponse } from '$lib/types/common';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import type { CPUInfo } from '$lib/types/info/cpu';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { renderWithIcon } from '$lib/utils/table';
	import { resource, watch } from 'runed';
	import { getContext, onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Data {
		node: string;
		ctId: number;
		jail: Jail | null;
		jailError: APIResponse | null;
		ram: RAMInfo | null;
		ramError: APIResponse | null;
		cpu: CPUInfo | null;
		cpuError: APIResponse | null;
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
				signal,
				preserveErrors: true
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

	let modals = $state({ ram: false, cpu: false, resourceLimits: false });
	let activeRows = $state<Row[] | null>(null);
	let activeRow = $derived<Row | null>(activeRows?.[0] ?? null);
	let query = $state('');
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: function (cell) {
					const value = cell.getValue();
					return value === 'Unlimited' ? renderWithIcon('mdi:infinity', '') : value;
				}
			}
		] as Column[],
		rows: currentJail
			? [
					{
						id: 'ram',
						property: 'RAM',
						value: currentJail.resourceLimits ? formatBytesBinary(currentJail.memory) : 'Unlimited'
					},
					{
						id: 'cpu',
						property: 'CPU',
						value: currentJail.resourceLimits ? currentJail.cores : 'Unlimited'
					}
				]
			: []
	});

	onMount(() => {
		if (data.jailError) handleAPIError(data.jailError);
		if (data.ramError) handleAPIError(data.ramError);
		if (data.cpuError) handleAPIError(data.cpuError);
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

{#snippet button(property: 'ram' | 'cpu' | 'resource-limits', title: string)}
	{#if property === 'resource-limits'}
		{#if !activeRows || activeRows.length === 0}
			<Button
				onclick={() => {
					modals.resourceLimits = true;
				}}
				size="sm"
				variant="outline"
				class="h-6.5 disabled:pointer-events-auto!"
				disabled={!canMutate}
				title={!canMutate ? 'Wait for the current jail operation to finish' : ''}
			>
				{#if currentJail?.resourceLimits}
					<SpanWithIcon
						icon="icon-[lsicon--disable-filled]"
						size="h-4 w-4"
						gap="gap-1"
						title="Disable Resource Limits"
					/>
				{:else}
					<SpanWithIcon
						icon="icon-[clarity--resource-pool-line]"
						size="h-4 w-4"
						gap="gap-1"
						title="Enable Resource Limits"
					/>
				{/if}
			</Button>
		{/if}
	{:else}
		{@const capacityAvailable = property === 'ram' ? !!data.ram : !!data.cpu}
		<Button
			onclick={() => {
				modals[property] = true;
			}}
			size="sm"
			variant="outline"
			class="h-6.5 disabled:pointer-events-auto!"
			title={!currentJail?.resourceLimits
				? 'Enable resource limits to edit'
				: !capacityAvailable
					? `Host ${title} information is unavailable`
					: !canMutate
						? 'Wait for the current jail operation to finish'
						: ''}
			disabled={!currentJail?.resourceLimits || !capacityAvailable || !canMutate}
		>
			<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit {title}" />
		</Button>
	{/if}
{/snippet}

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		{@render button('resource-limits', 'Resource Limits')}

		{#if activeRow?.property === 'RAM'}
			{@render button('ram', 'RAM')}
		{:else if activeRow?.property === 'CPU'}
			{@render button('cpu', 'CPU')}
		{/if}
	</div>

	{#if !currentJail}
		<div class="m-3 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm">
			Unable to load this jail's hardware configuration.
		</div>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name="jail-hardware-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if modals.ram && currentJail && data.ram}
	<RAM
		bind:open={modals.ram}
		ram={data.ram}
		jail={currentJail}
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if modals.cpu && currentJail && data.cpu}
	<CPU
		bind:open={modals.cpu}
		cpu={data.cpu}
		jail={currentJail}
		node={data.node}
		onSaved={reloadJail}
	/>
{/if}

{#if currentJail}
	<AlertDialog
		bind:open={modals.resourceLimits}
		customTitle={currentJail.resourceLimits
			? 'This will give unlimited resources to this jail, proceed with <b>caution!</b>'
			: 'This will enable resource limits for this jail, defaulting to <b>1 GiB RAM</b> and <b>1 vCPU</b>; you can change these later.'}
		keepOpenOnConfirm={true}
		actions={{
			onConfirm: async () => {
				if (!canMutate) return;
				const wasEnabled = currentJail.resourceLimits;
				const response = await updateResourceLimits(currentJail.ctId, !wasEnabled, {
					hostname: data.node
				});
				if (response.error) {
					handleAPIError(response);
					toast.error(`Failed to ${wasEnabled ? 'disable' : 'enable'} resource limits`, {
						position: 'bottom-center'
					});
					return;
				}

				await reloadJail();
				toast.success(`Resource limits ${wasEnabled ? 'disabled' : 'enabled'}`, {
					position: 'bottom-center'
				});
				modals.resourceLimits = false;
			},
			onCancel: () => {
				modals.resourceLimits = false;
			}
		}}
	/>
{/if}

<script lang="ts">
	import { getJailById } from '$lib/api/jail/jail';
	import AllowedOptions from '$lib/components/custom/Jail/Options/AllowedOptions.svelte';
	import LifecycleHooks from '$lib/components/custom/Jail/Options/LifecycleHooks.svelte';
	import StartOrder from '$lib/components/custom/Jail/Options/StartOrder.svelte';
	import TextEdit from '$lib/components/custom/Jail/Options/TextEdit.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Jail, JailState } from '$lib/types/jail/jail';
	import { updateCache } from '$lib/utils/http';
	import { generateNanoId, isBoolean } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		ctId: number;
		jail: Jail;
		jailState: JailState;
	}

	let { data }: { data: Data } = $props();

	const jail = resource(
		() => `jail-${data.ctId}`,
		async (key, prevKey, { signal }) => {
			const jail = await getJailById(data.ctId, 'ctid');
			updateCache(key, jail);
			return jail;
		},
		{
			initialValue: data.jail
		}
	);

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const value = cell.getValue();
					if (isBoolean(value)) {
						if (value === true || value === 'true') {
							return 'Yes';
						} else if (value === false || value === 'false') {
							return 'No';
						}
					}

					return value;
				}
			}
		],
		rows: [
			{
				id: generateNanoId('startOrder'),
				property: 'Start At Boot / Start Order',
				value: `${jail?.current.startAtBoot ? 'Yes' : 'No'} / ${jail?.current.startOrder || 0}`
			},
			{
				id: generateNanoId('fstab'),
				property: 'FSTab Entries',
				value: jail?.current.fstab
					? jail.current.fstab.split('\n')[0] + (jail.current.fstab.includes('\n') ? '…' : '')
					: '—'
			},
			{
				id: generateNanoId('devfsRules'),
				property: 'DevFS Ruleset',
				value: jail?.current.devfsRuleset
					? jail.current.devfsRuleset.split('\n')[0] +
						(jail.current.devfsRuleset.includes('\n') ? '…' : '')
					: '—'
			},
			{
				id: generateNanoId('additionalOptions'),
				property: 'Additional Options',
				value: jail?.current.additionalOptions
					? jail.current.additionalOptions.split('\n')[0] +
						(jail.current.additionalOptions.includes('\n') ? '…' : '')
					: '—'
			},
			{
				id: generateNanoId('allowedOptions'),
				property: 'Allowed Options',
				value: (() => {
					const options = jail?.current.allowedOptions || [];
					if (options.length === 0) return '—';
					if (options.length === 1) return options[0];
					return `${options[0]} (+${options.length - 1} more)`;
				})()
			},
			{
				id: generateNanoId('metadata'),
				property: 'Metadata',
				value: (() => {
					const meta = jail?.current.metadataMeta;
					const env = jail?.current.metadataEnv;

					if (!meta && !env) return '—';

					const preview = (v: string) => v.split('\n')[0] + (v.includes('\n') ? '…' : '');

					return [meta && `meta: ${preview(meta)}`, env && `env: ${preview(env)}`]
						.filter(Boolean)
						.join(' | ');
				})()
			},
			{
				id: generateNanoId('lifecycleHooks'),
				property: 'Lifecycle Hooks',
				value: (() => {
					const hooks = jail?.current.hooks || [];
					const enabledHooks = hooks.filter(
						(hook) => hook.enabled && hook.script && hook.script.trim() !== ''
					);

					if (enabledHooks.length === 0) return '—';
					if (enabledHooks.length === 1) return enabledHooks[0].phase;

					return `${enabledHooks[0].phase} (+${enabledHooks.length - 1} more)`;
				})()
			}
		]
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');

	let properties = $state({
		startOrder: { open: false },
		fstab: { open: false },
		devfsRules: { open: false },
		additionalOptions: { open: false },
		allowedOptions: { open: false },
		metadata: { open: false },
		lifecycleHooks: { open: false }
	});

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			jail.refetch();
			reload = false;
		}
	);
</script>

{#snippet button(
	type:
		| 'startOrder'
		| 'fstab'
		| 'devfsRules'
		| 'additionalOptions'
		| 'allowedOptions'
		| 'metadata'
		| 'lifecycleHooks',
	title: string
)}
	<Button
		onclick={() => {
			properties[type].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
	>
		<div class="flex items-center">
			<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
			<span>Edit {title}</span>
		</div>
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRows && activeRows?.length !== 0}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if activeRow.property === 'Start At Boot / Start Order'}
				{@render button('startOrder', 'Start At Boot / Start Order')}
			{:else if activeRow.property === 'FSTab Entries'}
				{@render button('fstab', 'FSTab Entries')}
			{:else if activeRow.property === 'DevFS Ruleset'}
				{@render button('devfsRules', 'DevFS Ruleset')}
			{:else if activeRow.property === 'Additional Options'}
				{@render button('additionalOptions', 'Additional Options')}
			{:else if activeRow.property === 'Allowed Options'}
				{@render button('allowedOptions', 'Allowed Options')}
			{:else if activeRow.property === 'Metadata'}
				{@render button('metadata', 'Metadata')}
			{:else if activeRow.property === 'Lifecycle Hooks'}
				{@render button('lifecycleHooks', 'Lifecycle Hooks')}
			{/if}
		</div>
	{/if}

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'jail-options-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.startOrder.open && jail.current}
	<StartOrder bind:open={properties.startOrder.open} jail={jail.current} bind:reload />
{/if}

{#if properties.fstab.open && jail.current}
	<TextEdit bind:open={properties.fstab.open} jail={jail.current} type="fstab" bind:reload />
{/if}

{#if properties.devfsRules.open && jail.current}
	<TextEdit
		bind:open={properties.devfsRules.open}
		jail={jail.current}
		type="devfsRules"
		bind:reload
	/>
{/if}

{#if properties.additionalOptions.open && jail.current}
	<TextEdit
		bind:open={properties.additionalOptions.open}
		jail={jail.current}
		type="additionalOptions"
		bind:reload
	/>
{/if}

{#if properties.allowedOptions.open && jail.current}
	<AllowedOptions bind:open={properties.allowedOptions.open} jail={jail.current} bind:reload />
{/if}

{#if properties.metadata.open && jail.current}
	<TextEdit bind:open={properties.metadata.open} jail={jail.current} type="metadata" bind:reload />
{/if}

{#if properties.lifecycleHooks.open && jail.current}
	<LifecycleHooks bind:open={properties.lifecycleHooks.open} jail={jail.current} bind:reload />
{/if}

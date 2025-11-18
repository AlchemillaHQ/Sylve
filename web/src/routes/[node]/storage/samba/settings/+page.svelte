<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSambaConfig, updateSambaConfig } from '$lib/api/samba/config';
	import SingleValueDialog from '$lib/components/custom/Dialog/SingleValue.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/types/components/tree-table';
	import type { Iface } from '$lib/types/network/iface';
	import type { SambaConfig } from '$lib/types/samba/config';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { useQueries, useQueryClient } from '@sveltestack/svelte-query';
	import { untrack } from 'svelte';
	import { toast } from 'svelte-sonner';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		sambaConfig: SambaConfig;
		interfaces: Iface[];
	}

	let { data }: { data: Data } = $props();

	const queryClient = useQueryClient();
	const results = useQueries([
		{
			queryKey: 'samba-config',
			queryFn: async () => {
				return await getSambaConfig();
			},
			keepPreviousData: true,
			initialData: data.sambaConfig,
			onSuccess: (data: SambaConfig) => {
				updateCache('samba-config', data);
			}
		},
		{
			queryKey: 'network-interfaces',
			queryFn: async () => {
				return await getInterfaces();
			},
			keepPreviousData: true,
			initialData: data.interfaces,
			onSuccess: (data: Iface[]) => {
				updateCache('network-interfaces', data);
			}
		}
	]);

	let reload = $state(false);

	$effect(() => {
		if (reload) {
			queryClient.refetchQueries('samba-config');
			queryClient.refetchQueries('network-interfaces');

			untrack(() => {
				reload = false;
			});
		}
	});

	let sambaConfig: SambaConfig = $derived($results[0].data as SambaConfig);
	let interfaces: Iface[] = $derived($results[1].data as Iface[]);
	let usableIfaces = $derived.by(() => {
		let filtered = [];
		for (const iface of interfaces) {
			if (iface.groups && iface.groups.length > 0) {
				if (!iface.groups.includes('tap')) {
					filtered.push(iface);
				}
			} else {
				filtered.push(iface);
			}
		}

		return filtered;
	});

	let options = {
		unixCharset: {
			value: (() => $state.snapshot(sambaConfig.unixCharset))(),
			open: false
		},
		workgroup: {
			value: (() => $state.snapshot(sambaConfig.workgroup))(),
			open: false
		},
		serverString: {
			value: (() => $state.snapshot(sambaConfig.serverString))(),
			open: false
		},
		interfaces: {
			value: (() => $state.snapshot(sambaConfig.interfaces))(),
			open: false
		},
		bindInterfaces: {
			value: (() => ($state.snapshot(sambaConfig.bindInterfacesOnly) ? 'Yes' : 'No'))(),
			open: false
		}
	};

	let properties = $state(options);
	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const row = cell.getRow();
					const property = row.getData().property;
					const value = cell.getValue();

					if (property === 'Interfaces') {
						const value = cell.getValue();
						const arr = Array.isArray(value) ? value : value.split(',');
						const formattedValue = arr.map((v: string) => {
							const iface = usableIfaces.find((i) => i.name === v);
							return iface ? (iface.description !== '' ? iface.description : iface.name) : v;
						});

						let v = '';
						if (formattedValue.length > 0) {
							for (const val of formattedValue) {
								const index = formattedValue.indexOf(val);
								v += `<span class=" focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive inline-flex w-fit shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-md border px-2 py-0.5 text-xs font-medium transition-[color,box-shadow] focus-visible:ring-[3px] [&>svg]:pointer-events-none [&>svg]:size-3 bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90 dark:border-transparent ${index > 0 ? 'ml-1.5' : ''}">${val}</span>`;
							}
						}

						return v;
					}

					return value;
				}
			}
		],
		rows: [
			{
				id: generateNanoId(`${sambaConfig.unixCharset}`),
				property: 'Unix Charset',
				value: sambaConfig.unixCharset
			},
			{
				id: generateNanoId(`${sambaConfig.workgroup}`),
				property: 'Workgroup',
				value: sambaConfig.workgroup
			},
			{
				id: generateNanoId(`${sambaConfig.serverString}`),
				property: 'Server String',
				value: sambaConfig.serverString
			},
			{
				id: generateNanoId(`${sambaConfig.interfaces}`),
				property: 'Interfaces',
				value: sambaConfig.interfaces
			},
			{
				id: generateNanoId(`${sambaConfig.bindInterfacesOnly}`),
				property: 'Bind Interfaces Only',
				value: sambaConfig.bindInterfacesOnly ? 'Yes' : 'No'
			}
		]
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');

	async function save() {
		const updatedConfig: Partial<SambaConfig> = {
			unixCharset: properties.unixCharset.value,
			workgroup: properties.workgroup.value,
			serverString: properties.serverString.value,
			interfaces: properties.interfaces.value,
			bindInterfacesOnly: properties.bindInterfaces.value === 'Yes'
		};

		const response = await updateSambaConfig(updatedConfig);

		reload = true;

		if (response.error) {
			properties = options;

			handleAPIError(response);
			toast.error('Failed to update Samba configuration', {
				position: 'bottom-center'
			});
		} else {
			toast.success('Samba configuration updated', {
				position: 'bottom-center'
			});
		}
	}
</script>

<div class="flex h-full w-full flex-col">
	{#if activeRows && activeRows?.length !== 0}
		<div class="flex h-10 w-full items-center gap-2 border-b p-2">
			{#if activeRow && activeRow.property !== ''}
				<Button
					onclick={() => {
						switch (activeRow.property) {
							case 'Unix Charset':
								properties.unixCharset.open = true;
								break;
							case 'Workgroup':
								properties.workgroup.open = true;
								break;
							case 'Server String':
								properties.serverString.open = true;
								break;
							case 'Interfaces':
								properties.interfaces.open = true;
								break;
							case 'Bind Interfaces Only':
								properties.bindInterfaces.open = true;
								break;
						}
					}}
					size="sm"
					variant="outline"
					class="h-6.5"
				>
					<div class="flex items-center">
						<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>

						<span>Edit {activeRow.property}</span>
					</div>
				</Button>
			{/if}
		</div>
	{/if}
	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable
			data={table}
			name={'hardware-tt'}
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

<SingleValueDialog
	bind:open={properties.workgroup.open}
	title="Workgroup"
	type="text"
	placeholder="Enter Workgroup"
	bind:value={properties.workgroup.value}
	onSave={() => {
		save();
		properties.workgroup.open = false;
	}}
/>

<SingleValueDialog
	bind:open={properties.unixCharset.open}
	title="Unix Charset"
	type="text"
	placeholder="Enter Unix Charset"
	bind:value={properties.unixCharset.value}
	onSave={() => {
		save();
		properties.unixCharset.open = false;
	}}
/>

<SingleValueDialog
	bind:open={properties.serverString.open}
	title="Server String"
	type="text"
	placeholder="Enter Server String"
	bind:value={properties.serverString.value}
	onSave={() => {
		save();
		properties.serverString.open = false;
	}}
/>

<SingleValueDialog
	bind:open={properties.bindInterfaces.open}
	title="Bind Interfaces Only"
	type="select"
	placeholder=""
	bind:value={properties.bindInterfaces.value}
	options={[
		{ label: 'Yes', value: 'Yes' },
		{ label: 'No', value: 'No' }
	]}
	onSave={() => {
		save();
		properties.bindInterfaces.open = false;
	}}
/>

<SingleValueDialog
	bind:open={properties.interfaces.open}
	title="Interfaces"
	type="combobox"
	placeholder="Select Interfaces"
	bind:value={properties.interfaces.value}
	options={usableIfaces.map((iface) => ({
		label: iface.description !== '' ? iface.description : iface.name,
		value: iface.name
	}))}
	onSave={() => {
		save();
		properties.interfaces.open = false;
	}}
/>

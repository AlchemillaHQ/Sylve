<script lang="ts">
	import { getInterfaces } from '$lib/api/network/iface';
	import { getSambaConfig } from '$lib/api/samba/config';
	import Config from '$lib/components/custom/Samba/Config.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Iface } from '$lib/types/network/iface';
	import type { SambaConfig } from '$lib/types/samba/config';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		sambaConfig: SambaConfig;
		interfaces: Iface[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let sambaConfig = resource(
		() => 'samba-config',
		async () => {
			const result = await getSambaConfig();
			updateCache('samba-config', result);
			return result;
		},
		{
			initialValue: data.sambaConfig
		}
	);

	// svelte-ignore state_referenced_locally
	let networkInterfaces = resource(
		() => 'network-interfaces',
		async () => {
			const result = await getInterfaces();
			updateCache('network-interfaces', result);
			return result;
		},
		{
			initialValue: data.interfaces
		}
	);

	let reload = $state(false);

	watch(
		() => reload,
		(value) => {
			if (value) {
				sambaConfig.refetch();
				networkInterfaces.refetch();
				reload = false;
			}
		}
	);

	let usableIfaces = $derived.by(() => {
		let filtered = [];
		if (isAPIResponse(networkInterfaces.current)) return [];

		for (const iface of networkInterfaces.current) {
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
				id: generateNanoId(`${sambaConfig.current.unixCharset}`),
				property: 'Unix Charset',
				value: sambaConfig.current.unixCharset
			},
			{
				id: generateNanoId(`${sambaConfig.current.workgroup}`),
				property: 'Workgroup',
				value: sambaConfig.current.workgroup
			},
			{
				id: generateNanoId(`${sambaConfig.current.serverString}`),
				property: 'Server String',
				value: sambaConfig.current.serverString
			},
			{
				id: generateNanoId(`${sambaConfig.current.interfaces}`),
				property: 'Interfaces',
				value: sambaConfig.current.interfaces
			},
			{
				id: generateNanoId(`${sambaConfig.current.bindInterfacesOnly}`),
				property: 'Bind Interfaces Only',
				value: sambaConfig.current.bindInterfacesOnly ? 'Yes' : 'No'
			},
			{
				id: generateNanoId(`${sambaConfig.current.appleExtensions}`),
				property: 'Apple Extensions',
				value: sambaConfig.current.appleExtensions ? 'Yes' : 'No'
			}
		]
	});

	let query = $state('');
	let modalOpen = $state(false);
</script>

<div class="flex h-full w-full flex-col">
	<div class="flex h-10 w-full items-center gap-2 border-b p-2">
		<Search bind:query />

		<Button size="sm" variant="default" class="h-6" onclick={() => (modalOpen = true)}>
			<SpanWithIcon
				icon="icon-[hugeicons--system-update-01]"
				size="h-4 w-4"
				gap="gap-2"
				title="Update"
			/>
		</Button>
	</div>

	<div class="flex h-full flex-col overflow-hidden">
		<TreeTable data={table} name="samba-config-tt" multipleSelect={false} bind:query />
	</div>
</div>

<Config
	bind:open={modalOpen}
	bind:reload
	sambaConfig={sambaConfig.current}
	networkInterfaces={usableIfaces}
/>

<script lang="ts">
	import { getMdnsSettings } from '$lib/api/network/mdns';
	import { getInterfaces } from '$lib/api/network/iface';
	import Config from '$lib/components/custom/Network/MDNS/Config.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import Search from '$lib/components/custom/TreeTable/Search.svelte';
	import Button from '$lib/components/ui/button/button.svelte';
	import type { Iface } from '$lib/types/network/iface';
	import type { MdnsSettings } from '$lib/types/network/mdns';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { generateNanoId } from '$lib/utils/string';
	import { resource, watch } from 'runed';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		settings: MdnsSettings;
		interfaces: Iface[];
	}

	let { data }: { data: Data } = $props();

	// svelte-ignore state_referenced_locally
	let settings = resource(
		() => 'mdns-settings',
		async (key) => {
			const res = await getMdnsSettings();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.settings }
	);

	// svelte-ignore state_referenced_locally
	let networkInterfaces = resource(
		() => 'network-interfaces',
		async (key) => {
			const res = await getInterfaces();
			updateCache(key, res);
			return res;
		},
		{ initialValue: data.interfaces }
	);

	let reload = $state(false);

	watch(
		() => reload,
		(current) => {
			if (current) {
				settings.refetch();
				networkInterfaces.refetch();
				reload = false;
			}
		}
	);

	let usableIfaces = $derived.by(() => {
		if (isAPIResponse(networkInterfaces.current)) return [];

		const filtered: Iface[] = [];
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

	let query = $state('');
	let modalOpen = $state(false);

	let tableData = $derived.by(() => {
		const columns = [
			{ field: 'property', title: 'Property' },
			{
				field: 'value',
				title: 'Value',
				formatter: (cell: CellComponent) => {
					const property = cell.getRow().getData().property;

					if (property === 'Interfaces') {
						const value = cell.getValue() as string;
						if (!value) {
							return '<span class="focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive inline-flex w-fit shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-md border px-2 py-0.5 text-xs font-medium transition-[color,box-shadow] focus-visible:ring-[3px] [&>svg]:pointer-events-none [&>svg]:size-3 bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90 dark:border-transparent">All</span>';
						}

						const arr = value.split(',').map((v: string) => v.trim()).filter(Boolean);
						let html = '';
						for (let i = 0; i < arr.length; i++) {
							const iface = usableIfaces.find((ifc) => ifc.name === arr[i]);
							const label = iface ? (iface.description !== '' ? iface.description : iface.name) : arr[i];
							html += `<span class="focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive inline-flex w-fit shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-md border px-2 py-0.5 text-xs font-medium transition-[color,box-shadow] focus-visible:ring-[3px] [&>svg]:pointer-events-none [&>svg]:size-3 bg-secondary text-secondary-foreground [a&]:hover:bg-secondary/90 dark:border-transparent${i > 0 ? ' ml-1.5' : ''}">${label}</span>`;
						}
						return html;
					}

					return cell.getValue();
				}
			}
		];

		const s = settings.current;
		if (!s) return { columns, rows: [] };

		const rows = [
			{ id: generateNanoId('interfaces'), property: 'Interfaces', value: s.interfaces || '' },
			{ id: generateNanoId('hostname'), property: 'Hostname', value: s.hostname || 'System Hostname' }
		];

		return { columns, rows };
	});
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
		<TreeTable data={tableData} name="tt-mdns-settings" multipleSelect={false} bind:query />
	</div>
</div>

<Config
	bind:open={modalOpen}
	bind:reload
	mdnsSettings={settings.current}
	networkInterfaces={usableIfaces}
/>

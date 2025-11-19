<script lang="ts">
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getVMDomain, getVMs } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import CPU from '$lib/components/custom/VM/Hardware/CPU.svelte';
	import PCIDevices from '$lib/components/custom/VM/Hardware/PCIDevices.svelte';
	import RAM from '$lib/components/custom/VM/Hardware/RAM.svelte';
	import VNC from '$lib/components/custom/VM/Hardware/VNC.svelte';
	import Serial from '$lib/components/custom/VM/Options/Serial.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { Row } from '$lib/components/ui/table';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { CPUPin, VM, VMDomain } from '$lib/types/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable } from '$lib/utils/numbers';
	import { generateNanoId } from '$lib/utils/string';
	import { useQueries } from '@sveltestack/svelte-query';
	import type { CellComponent } from 'tabulator-tables';

	interface Data {
		vmId: number;
		vms: VM[];
		vm: VM;
		ram: RAMInfo;
		domain: VMDomain;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
	}

	let { data }: { data: Data } = $props();
	const results = useQueries([
		{
			queryKey: ['vm-list'],
			queryFn: async () => {
				return await getVMs();
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.vms,
			onSuccess: (data: VM[]) => {
				updateCache('vm-list', data);
			}
		},
		{
			queryKey: ['pciDevices'],
			queryFn: async () => {
				return (await getPCIDevices()) as PCIDevice[];
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.pciDevices,
			onSuccess: (data: PCIDevice[]) => {
				updateCache('pciDevices', data);
			}
		},
		{
			queryKey: ['pptDevices'],
			queryFn: async () => {
				return (await getPPTDevices()) as PPTDevice[];
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.pptDevices,
			onSuccess: (data: PPTDevice[]) => {
				updateCache('pptDevices', data);
			}
		},
		{
			queryKey: [`vmDomain-${data.vmId}`],
			queryFn: async () => {
				return await getVMDomain(data.vmId);
			},
			refetchInterval: 1000,
			keepPreviousData: true,
			initialData: data.domain,
			onSuccess: (updated: VMDomain) => {
				updateCache(`vmDomain-${data.vmId}`, updated);
			}
		}
	]);

	let vms: VM[] = $derived($results[0].data ? $results[0].data : data.vms);
	let vm: VM | null = $derived(
		vms && data.vm ? (vms.find((v: VM) => v.vmId === data.vm.vmId) ?? null) : null
	);
	let pciDevices: PCIDevice[] = $derived($results[1].data as PCIDevice[]);
	let pptDevices: PPTDevice[] = $derived($results[2].data as PPTDevice[]);
	let domain = $derived($results[3].data as VMDomain);

	let options = {
		cpu: {
			sockets: data.vm.cpuSockets,
			cores: data.vm.cpuCores,
			threads: data.vm.cpuThreads,
			pinning: data.vm.cpuPinning,
			vCPUs: data.vm.cpuSockets * data.vm.cpuCores * data.vm.cpuThreads,
			open: false,
			pinnedCPUs: [] as CPUPin[]
		},
		ram: {
			value: data.vm.ram,
			open: false
		},
		vnc: {
			enabled: data.vm.vncEnabled,
			resolution: data.vm.vncResolution,
			port: data.vm.vncPort,
			password: data.vm.vncPassword,
			open: false
		},
		pciDevices: {
			open: false,
			value: data.vm.pciDevices
		},
		serial: { open: false }
	};

	let properties = $state(options);

	$effect(() => {
		if (vm) {
			properties.cpu.sockets = vm.cpuSockets;
			properties.cpu.cores = vm.cpuCores;
			properties.cpu.threads = vm.cpuThreads;
			properties.cpu.vCPUs = vm.cpuSockets * vm.cpuCores * vm.cpuThreads;
			properties.ram.value = vm.ram;
			properties.vnc.enabled = vm.vncEnabled;
			properties.vnc.port = vm.vncPort;
			properties.vnc.password = vm.vncPassword;
			properties.vnc.resolution = vm.vncResolution;
			properties.pciDevices.value = vm.pciDevices;
		}
	});

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');

	let table = $derived({
		columns: [
			{ title: 'Property', field: 'property' },
			{
				title: 'Value',
				field: 'value',
				formatter: (cell: CellComponent) => {
					const row = cell.getRow();
					const value = cell.getValue();

					if (row.getData().property === 'PCI Devices') {
						if (!Array.isArray(value) || value.length === 0) return '-';

						const selected = pptDevices.filter((d) => value.includes(d.id));
						const labels: string[] = [];

						for (const dev of selected) {
							const [busStr, deviceStr, functionStr] = dev.deviceID.split('/');
							const bus = Number(busStr);
							const deviceC = Number(deviceStr);
							const functionC = Number(functionStr);

							for (const pci of pciDevices) {
								if (pci.bus === bus && pci.device === deviceC && pci['function'] === functionC) {
									labels.push(`${pci.names.vendor} ${pci.names.device}`);
								}
							}
						}

						if (labels.length === 0) return '-';

						return `<div class="flex flex-col gap-1">${labels
							.map((t) => `<div>${t}</div>`)
							.join('')}</div>`;
					} else {
						return value;
					}
				}
			}
		],
		rows: [
			{
				id: generateNanoId(`${properties.cpu.vCPUs}-vcpus`),
				property: 'vCPUs',
				value: properties.cpu.vCPUs
			},
			{
				id: generateNanoId(`${properties.ram.value}-ram`),
				property: 'RAM',
				value: bytesToHumanReadable(properties.ram.value)
			},
			{
				id: generateNanoId(`${properties.vnc.port}-vnc-port`),
				property: 'VNC',
				value: `${properties.vnc.enabled ? 'Enabled' : 'Disabled'} / ${properties.vnc.resolution} / ${properties.vnc.port}`
			},
			{
				id: generateNanoId('serial'),
				property: 'Serial Console',
				value: vm?.serial ? 'Enabled' : 'Disabled'
			},
			{
				id: generateNanoId(`${vm?.name}-pci-devices`),
				property: 'PCI Devices',
				value: properties.pciDevices.value || []
			}
		]
	});

	let reload = $state(false);
</script>

{#snippet button(property: 'ram' | 'cpu' | 'vnc' | 'pciDevices' | 'serial', title: string)}
	<Button
		onclick={() => {
			properties[property].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
		title={domain.status === 'Shutoff' ? '' : `${title} can only be edited when the VM is shut off`}
		disabled={domain.status ? domain.status !== 'Shutoff' : false}
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
			{#if activeRow && activeRow.property === 'RAM'}
				{@render button('ram', 'RAM')}
			{/if}

			{#if activeRow && activeRow.property === 'vCPUs'}
				{@render button('cpu', 'CPU')}
			{/if}

			{#if activeRow && activeRow.property === 'VNC'}
				{@render button('vnc', 'VNC')}
			{/if}

			{#if activeRow && activeRow.property === 'PCI Devices'}
				{@render button('pciDevices', 'PCI Devices')}
			{/if}

			{#if activeRow && activeRow.property === 'Serial Console'}
				{@render button('serial', 'Serial Console')}
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

{#if properties.ram.open}
	<RAM bind:open={properties.ram.open} ram={data.ram} {vm} />
{/if}

{#if properties.cpu.open}
	<CPU bind:open={properties.cpu.open} {vm} {vms} bind:pinnedCPUs={properties.cpu.pinnedCPUs} />
{/if}

{#if properties.vnc.open}
	<VNC bind:open={properties.vnc.open} {vm} {vms} />
{/if}

{#if properties.pciDevices.open}
	<PCIDevices bind:open={properties.pciDevices.open} {vm} {pciDevices} {pptDevices} />
{/if}

{#if properties.serial.open && vm}
	<Serial bind:open={properties.serial.open} {vm} bind:reload />
{/if}

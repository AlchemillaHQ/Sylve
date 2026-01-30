<script lang="ts">
	import { storage } from '$lib';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getVMDomain, getVMs } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import CPU from '$lib/components/custom/VM/Hardware/CPU.svelte';
	import PCIDevices from '$lib/components/custom/VM/Hardware/PCIDevices.svelte';
	import RAM from '$lib/components/custom/VM/Hardware/RAM.svelte';
	import VNC from '$lib/components/custom/VM/Hardware/VNC.svelte';
	import Serial from '$lib/components/custom/VM/Options/Serial.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { CPUPin, VM, VMDomain } from '$lib/types/vm/vm';
	import { updateCache } from '$lib/utils/http';
	import { bytesToHumanReadable } from '$lib/utils/numbers';
	import { generateNanoId } from '$lib/utils/string';
	import type { CellComponent } from 'tabulator-tables';
	import { resource, useInterval } from 'runed';
	import { untrack } from 'svelte';
	import { core } from 'zod/v4';
	import type { Row } from '$lib/types/components/tree-table';
	import TPM from '$lib/components/custom/VM/Hardware/TPM.svelte';

	interface Data {
		rid: number;
		vms: VM[];
		vm: VM;
		ram: RAMInfo;
		domain: VMDomain;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
	}

	let { data }: { data: Data } = $props();

	const vms = resource(
		() => 'vm-list',
		async (key) => {
			const result = await getVMs();
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.vms
		}
	);

	const pciDevices = resource(
		() => 'pciDevices',
		async (key) => {
			const result = (await getPCIDevices()) as PCIDevice[];
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.pciDevices
		}
	);

	const pptDevices = resource(
		() => 'pptDevices',
		async (key) => {
			const result = (await getPPTDevices()) as PPTDevice[];
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.pptDevices
		}
	);

	const domain = resource(
		() => `vm-domain-${data.rid}`,
		async (key) => {
			const result = await getVMDomain(Number(data.rid));
			updateCache(key, result);
			return result;
		},
		{
			lazy: true,
			initialValue: data.domain
		}
	);

	useInterval(() => 1000, {
		callback: () => {
			if (storage.visible) {
				vms.refetch();
				pciDevices.refetch();
				pptDevices.refetch();
				domain.refetch();
			}
		}
	});

	$effect(() => {
		if (storage.visible) {
			untrack(() => {
				vms.refetch();
				pciDevices.refetch();
				pptDevices.refetch();
				domain.refetch();
			});
		}
	});

	let vm: VM | null = $derived(
		vms && data.vm ? (vms.current.find((v: VM) => v.rid === data.vm.rid) ?? null) : null
	);

	// svelte-ignore state_referenced_locally
	let options = {
		cpu: {
			sockets: data.vm.cpuSockets,
			cores: data.vm.cpuCores,
			threads: data.vm.cpuThreads,
			pinning: data.vm.cpuPinning,
			vCPUs: data.vm.cpuSockets * data.vm.cpuCores * data.vm.cpuThreads,
			open: false,
			pinnedCPUs:
				data.vm.cpuPinning?.map((pin) => {
					return {
						socket: pin.hostSocket,
						cores: pin.hostCpu
					};
				}) || ([] as CPUPin[])
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
		serial: { open: false },
		tpmEmulation: { open: false }
	};

	let properties = $state(options);

	$effect(() => {
		if (vm) {
			properties.cpu.sockets = vm.cpuSockets;
			properties.cpu.cores = vm.cpuCores;
			properties.cpu.threads = vm.cpuThreads;
			properties.cpu.vCPUs = vm.cpuSockets * vm.cpuCores * vm.cpuThreads;
			properties.cpu.pinning = vm.cpuPinning;
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

						const selected = pptDevices.current.filter((d) => value.includes(d.id));
						const labels: string[] = [];

						for (const dev of selected) {
							const [busStr, deviceStr, functionStr] = dev.deviceID.split('/');
							const bus = Number(busStr);
							const deviceC = Number(deviceStr);
							const functionC = Number(functionStr);

							for (const pci of pciDevices.current) {
								if (pci.bus === bus && pci.device === deviceC && pci['function'] === functionC) {
									labels.push(`${pci.names.vendor} ${pci.names.device}`);
								}
							}
						}

						if (labels.length === 0) return '-';

						return `<div class="flex flex-col gap-1">${labels
							.map((t) => `<div>${t}</div>`)
							.join('')}</div>`;
					} else if (row.getData().property === 'VNC') {
						return `
                            <span class="flex flex-col text-sm leading-tight">
                                <span>
                                    ${properties.vnc.enabled ? 'Enabled' : 'Disabled'}
                                </span>
                                <span>
                                    ${properties.vnc.resolution} / ${properties.vnc.port}
                                </span>
                                <span >
                                    ${properties.vnc.password || 'No Password'}
                                </span>
                            </span>
                        `;
					} else {
						return value;
					}
				},
				copyOnClick: (row: Row) => {
					try {
						const property = row.getData().property;
						if (property === 'VNC') {
							return true;
						}

						return false;
					} catch (e) {
						console.error(e);
						return false;
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
				value: properties.vnc,
				toCopy: properties.vnc.enabled
					? `vnc://${properties.vnc.password ? `:${properties.vnc.password}@` : ''}${window.location.hostname}:${properties.vnc.port}`
					: ''
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
			},
			{
				id: generateNanoId('tpm-emulation'),
				property: 'TPM Emulation',
				value: vm?.tpmEmulation ? 'Enabled' : 'Disabled'
			}
		]
	});

	let reload = $state(false);
</script>

{#snippet button(
	property: 'ram' | 'cpu' | 'vnc' | 'pciDevices' | 'serial' | 'tpmEmulation',
	title: string
)}
	<Button
		onclick={() => {
			properties[property].open = true;
		}}
		size="sm"
		variant="outline"
		class="h-6.5"
		title={domain.current.status === 'Shutoff'
			? ''
			: `${title} can only be edited when the VM is shut off`}
		disabled={domain.current.status ? domain.current.status !== 'Shutoff' : false}
	>
		<div class="flex items-center">
			<span class="icon-[mdi--pencil] mr-1 h-4 w-4"></span>
			<span>Edit {title}</span>
		</div>
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRows && activeRows?.length !== 0 && domain.current.status && domain.current.status === 'Shutoff'}
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

			{#if activeRow && activeRow.property === 'TPM Emulation'}
				{@render button('tpmEmulation', 'TPM Emulation')}
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
	<CPU
		bind:open={properties.cpu.open}
		{vm}
		vms={vms.current}
		bind:pinnedCPUs={properties.cpu.pinnedCPUs}
	/>
{/if}

{#if properties.vnc.open}
	<VNC bind:open={properties.vnc.open} {vm} vms={vms.current} />
{/if}

{#if properties.pciDevices.open}
	<PCIDevices
		bind:open={properties.pciDevices.open}
		{vm}
		pciDevices={pciDevices.current}
		pptDevices={pptDevices.current}
	/>
{/if}

{#if properties.serial.open && vm}
	<Serial bind:open={properties.serial.open} {vm} bind:reload />
{/if}

{#if properties.tpmEmulation.open && vm}
	<TPM bind:open={properties.tpmEmulation.open} {vm} bind:reload />
{/if}

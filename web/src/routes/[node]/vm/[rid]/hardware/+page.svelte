<script lang="ts">
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getVMsResult } from '$lib/api/vm/vm';
	import TreeTable from '$lib/components/custom/TreeTable.svelte';
	import CPU from '$lib/components/custom/VM/Hardware/CPU.svelte';
	import PCIDevices from '$lib/components/custom/VM/Hardware/PCIDevices.svelte';
	import RAM from '$lib/components/custom/VM/Hardware/RAM.svelte';
	import VNC from '$lib/components/custom/VM/Hardware/VNC.svelte';
	import Serial from '$lib/components/custom/VM/Options/Serial.svelte';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import type { APIResponse } from '$lib/types/common';
	import type { RAMInfo } from '$lib/types/info/ram';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import { type VMCPUPinning, type CPUPin, type VM, type VMDomain } from '$lib/types/vm/vm';
	import { formatBytesBinary } from '$lib/utils/bytes';
	import { handleAPIError, isAPIResponse, updateCache } from '$lib/utils/http';
	import { escapeHTML, generateNanoId } from '$lib/utils/string';
	import type { CellComponent, RowComponent } from 'tabulator-tables';
	import { resource, watch } from 'runed';
	import type { Column, Row } from '$lib/types/components/tree-table';
	import TPM from '$lib/components/custom/VM/Hardware/TPM.svelte';
	import { getContext, onMount, untrack } from 'svelte';

	interface Data {
		vms: VM[];
		vm: VM;
		node: string;
		rid: number;
		ram: RAMInfo;
		pciDevices: PCIDevice[];
		pptDevices: PPTDevice[];
		loadErrors: APIResponse[];
	}

	let { data }: { data: Data } = $props();
	const initialData = untrack(() => data);

	const domain = getContext<{ current: VMDomain | null; refetch(): void }>('vmDomain');

	const lastVMsByNode: Record<string, VM[]> = Object.create(null);
	lastVMsByNode[initialData.node] = initialData.vms;
	const vms = resource(
		() => data.node,
		async (node) => {
			const result = await getVMsResult({ hostname: node });
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastVMsByNode[node] ?? [data.vm];
			}
			lastVMsByNode[node] = result;
			await updateCache('vm-list', result, node);
			return result;
		},
		{
			initialValue: initialData.vms
		}
	);

	const lastPCIDevicesByNode: Record<string, PCIDevice[]> = Object.create(null);
	lastPCIDevicesByNode[initialData.node] = initialData.pciDevices;
	const pciDevices = resource(
		() => data.node,
		async (node): Promise<PCIDevice[]> => {
			const result = await getPCIDevices(node);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastPCIDevicesByNode[node] ?? [];
			}
			lastPCIDevicesByNode[node] = result;
			await updateCache('pciDevices', result, node);
			return result;
		},
		{
			initialValue: initialData.pciDevices
		}
	);

	const lastPPTDevicesByNode: Record<string, PPTDevice[]> = Object.create(null);
	lastPPTDevicesByNode[initialData.node] = initialData.pptDevices;
	const pptDevices = resource(
		() => data.node,
		async (node): Promise<PPTDevice[]> => {
			const result = await getPPTDevices(node);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return lastPPTDevicesByNode[node] ?? [];
			}
			lastPPTDevicesByNode[node] = result;
			await updateCache('pptDevices', result, node);
			return result;
		},
		{
			initialValue: initialData.pptDevices
		}
	);

	let vm: VM | null = $derived(
		vms.current.find((candidate) => candidate.rid === data.rid) ?? data.vm
	);

	let reload = $state(false);

	watch(
		() => reload,
		(shouldReload) => {
			if (shouldReload) {
				reload = false;
				vms.refetch();
			}
		}
	);

	onMount(() => {
		for (const loadError of data.loadErrors) handleAPIError(loadError);
	});

	let isLifecycleActive = $derived(!!domain.current?.pendingAction);
	let isDomainShutoff = $derived(
		!isLifecycleActive &&
			String(domain.current?.status || '')
				.trim()
				.toLowerCase() === 'shutoff'
	);

	// svelte-ignore state_referenced_locally
	let options = {
		cpu: {
			sockets: data.vm?.cpuSockets || 0,
			cores: data.vm?.cpuCores || 0,
			threads: data.vm?.cpuThreads || 0,
			pinning: data.vm?.cpuPinning || ([] as VMCPUPinning[]),
			vCPUs: (data.vm?.cpuSockets || 0) * (data.vm?.cpuCores || 0) * (data.vm?.cpuThreads || 0),
			open: false,
			pinnedCPUs:
				data.vm?.cpuPinning?.map((pin) => {
					return {
						socket: pin.hostSocket,
						cores: pin.hostCpu
					};
				}) || ([] as CPUPin[])
		},
		ram: {
			value: data.vm?.ram || 1024,
			open: false
		},
		vnc: {
			enabled: data.vm?.vncEnabled,
			resolution: data.vm?.vncResolution,
			port: data.vm?.vncPort,
			bind: data.vm?.vncBind || '127.0.0.1',
			password: data.vm?.vncPassword,
			wait: data.vm?.vncWait ?? false,
			open: false
		},
		pciDevices: {
			open: false,
			value: data.vm?.pciDevices
		},
		serial: { open: false },
		tpmEmulation: { open: false }
	};

	let properties = $state(options);

	watch(
		() => vm,
		(currentVM) => {
			if (currentVM) {
				properties.cpu.sockets = currentVM.cpuSockets;
				properties.cpu.cores = currentVM.cpuCores;
				properties.cpu.threads = currentVM.cpuThreads;
				properties.cpu.vCPUs = currentVM.cpuSockets * currentVM.cpuCores * currentVM.cpuThreads;
				properties.cpu.pinning = currentVM.cpuPinning ?? [];
				properties.cpu.pinnedCPUs =
					currentVM.cpuPinning?.map((pin) => ({
						socket: pin.hostSocket,
						cores: [...pin.hostCpu]
					})) ?? [];
				properties.ram.value = currentVM.ram;
				properties.vnc.enabled = currentVM.vncEnabled;
				properties.vnc.port = currentVM.vncPort;
				properties.vnc.bind = currentVM.vncBind;
				properties.vnc.password = currentVM.vncPassword;
				properties.vnc.resolution = currentVM.vncResolution;
				properties.vnc.wait = currentVM.vncWait ?? false;
				properties.pciDevices.value = currentVM.pciDevices;
			}
		}
	);

	let activeRows: Row[] | null = $state(null);
	let activeRow: Row | null = $derived(activeRows ? (activeRows[0] as Row) : ({} as Row));
	let query = $state('');

	function getValue(
		property: 'cpu' | 'ram' | 'vnc' | 'serial' | 'pci-devices' | 'tpm-emulation'
	): string {
		if (property === 'cpu') {
			const s = properties.cpu.sockets || 0;
			const c = properties.cpu.cores || 0;
			const t = properties.cpu.threads || 0;
			const total = s * c * t;
			let label = `<span class="text-sm">${s} Socket × ${c} Core × ${t} Thread`;
			if (total > 0) label += ` (${total} vCPU${total > 1 ? 's' : ''})`;

			const pinning = properties.cpu.pinning ?? [];
			if (pinning.length > 0) {
				const lines = pinning.map((p) =>
					escapeHTML(`Socket ${p.hostSocket}: [${(p.hostCpu ?? []).join(', ')}]`)
				);
				label += `<br /><span class="text-muted-foreground text-xs">Pinned: ${lines.join(', ')}</span>`;
			}
			label += `</span>`;

			return label;
		} else if (property === 'ram') {
			return formatBytesBinary(properties.ram.value);
		} else if (property === 'vnc') {
			const enabled = properties.vnc.enabled;
			const resolution = escapeHTML(String(properties.vnc.resolution ?? ''));
			const port = escapeHTML(String(properties.vnc.port ?? ''));
			const bind = escapeHTML(String(properties.vnc.bind || '127.0.0.1'));
			const password = properties.vnc.password
				? escapeHTML(String(properties.vnc.password))
				: 'No Password';

			const icon = enabled
				? `icon-[mdi--check-circle] text-green-500`
				: `icon-[mdi--close-circle] text-red-500`;

			const wait = properties.vnc.wait
				? `
                    <span class="inline-flex items-center gap-1">
                        <span class="icon-[mdi--timer-sand]"></span>
                        <span>Wait</span>
                    </span>
                `
				: '';

			return `
            <span class="flex flex-col text-sm leading-tight gap-1">
                <div class="flex items-center gap-2">
                    <span class="inline-flex items-center gap-1">
                        <span class="${icon}"></span>
                        <span>${enabled ? 'Enabled' : 'Disabled'}</span>
                    </span>

                    ${wait ? `<span class="text-muted-foreground">|</span>${wait}` : ''}
                </div>

				<span>
					${resolution} / ${port}
				</span>

				<span>
					Bind: ${bind}
				</span>

				<span>
					${password}
                </span>
            </span>
        `;
		} else if (property === 'serial') {
			const enabled = vm?.serial;

			const icon = enabled
				? `icon-[mdi--check-circle] text-green-500`
				: `icon-[mdi--close-circle] text-red-500`;

			return `
            <span class="inline-flex items-center gap-1">
                <span class="${icon}"></span>
                <span>${enabled ? 'Enabled' : 'Disabled'}</span>
            </span>
        `;
		} else if (property === 'pci-devices') {
			if (!Array.isArray(properties.pciDevices.value) || properties.pciDevices.value.length === 0)
				return '-';

			const selected = pptDevices.current.filter((d) =>
				(properties.pciDevices.value as unknown as Array<string | number>)
					.map(String)
					.includes(String(d.id))
			);
			const labels: string[] = [];

			for (const dev of selected) {
				const [busStr, deviceStr, functionStr] = dev.deviceID.split('/');
				const bus = Number(busStr);
				const deviceC = Number(deviceStr);
				const functionC = Number(functionStr);

				for (const pci of pciDevices.current) {
					if (
						pci.domain === dev.domain &&
						pci.bus === bus &&
						pci.device === deviceC &&
						pci['function'] === functionC
					) {
						labels.push(escapeHTML(`${pci.names.vendor} ${pci.names.device}`));
					}
				}
			}

			if (labels.length === 0) return '-';

			return `<div class="flex flex-col gap-1">${labels
				.map((t) => `<div>${t}</div>`)
				.join('')}</div>`;
		} else if (property === 'tpm-emulation') {
			const enabled = vm?.tpmEmulation;

			const icon = enabled
				? `icon-[mdi--check-circle] text-green-500`
				: `icon-[mdi--close-circle] text-red-500`;

			return `
            <span class="inline-flex items-center gap-1">
                <span class="${icon}"></span>
                <span>${enabled ? 'Enabled' : 'Disabled'}</span>
            </span>
        `;
		}

		return '';
	}

	function vncCopyURI(): string {
		if (!properties.vnc.enabled) return '';
		const rawHost = String(properties.vnc.bind || data.node);
		const host = rawHost.includes(':') && !rawHost.startsWith('[') ? `[${rawHost}]` : rawHost;
		const credentials = properties.vnc.password
			? `:${encodeURIComponent(String(properties.vnc.password))}@`
			: '';
		return `vnc://${credentials}${host}:${properties.vnc.port}`;
	}

	let table = $derived.by(() => {
		return {
			columns: [
				{ title: 'Property', field: 'property' },
				{
					title: 'Value',
					field: 'value',
					copyOnClick: (row: RowComponent) => {
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
					},
					formatter: (cell: CellComponent) => {
						return cell.getValue();
					}
				}
			] as Column[],
			rows: [
				{
					id: generateNanoId(`${properties.cpu.vCPUs}-vcpus`),
					property: 'vCPUs',
					value: getValue('cpu')
				},
				{
					id: generateNanoId(`${properties.ram.value}-ram`),
					property: 'RAM',
					value: getValue('ram')
				},
				{
					id: generateNanoId(`${properties.vnc.port}-vnc-port`),
					property: 'VNC',
					value: getValue('vnc'),
					toCopy: vncCopyURI()
				},
				{
					id: generateNanoId('serial'),
					property: 'Serial Console',
					value: getValue('serial')
				},
				{
					id: generateNanoId(`${vm?.name}-pci-devices`),
					property: 'PCI Devices',
					value: getValue('pci-devices')
				},
				{
					id: generateNanoId('tpm-emulation'),
					property: 'TPM Emulation',
					value: getValue('tpm-emulation')
				}
			] as Row[]
		};
	});
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
		title={isDomainShutoff ? '' : `${title} can only be edited when the VM is shut off`}
		disabled={!isDomainShutoff}
	>
		<SpanWithIcon icon="icon-[mdi--pencil]" size="h-4 w-4" gap="gap-1" title="Edit {title}" />
	</Button>
{/snippet}

<div class="flex h-full w-full flex-col">
	{#if activeRows && activeRows?.length !== 0 && isDomainShutoff}
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
			name="vm-hardware-tt"
			bind:parentActiveRow={activeRows}
			multipleSelect={false}
			bind:query
		/>
	</div>
</div>

{#if properties.ram.open}
	<RAM bind:open={properties.ram.open} node={data.node} ram={data.ram} {vm} bind:reload />
{/if}

{#if properties.cpu.open}
	<CPU
		bind:open={properties.cpu.open}
		node={data.node}
		{vm}
		vms={vms.current}
		bind:pinnedCPUs={properties.cpu.pinnedCPUs}
		bind:reload
	/>
{/if}

{#if properties.vnc.open}
	<VNC bind:open={properties.vnc.open} node={data.node} {vm} vms={vms.current} bind:reload />
{/if}

{#if properties.pciDevices.open}
	<PCIDevices
		bind:open={properties.pciDevices.open}
		node={data.node}
		{vm}
		pciDevices={pciDevices.current}
		pptDevices={pptDevices.current}
		bind:reload
	/>
{/if}

{#if properties.serial.open && vm}
	<Serial bind:open={properties.serial.open} node={data.node} {vm} bind:reload />
{/if}

{#if properties.tpmEmulation.open && vm}
	<TPM bind:open={properties.tpmEmulation.open} node={data.node} {vm} bind:reload />
{/if}

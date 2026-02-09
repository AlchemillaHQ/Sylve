<script lang="ts">
	import { getSwitches } from '$lib/api/network/switch';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getDownloadsByUType } from '$lib/api/utilities/downloader';
	import { getVMs, newVM } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { PCIDevice } from '$lib/types/system/pci';
	import { generatePassword } from '$lib/utils/string';
	import { getNextId, isValidCreateData } from '$lib/utils/vm/vm';
	import Advanced from './Advanced.svelte';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getJails } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { reload as reloadStore } from '$lib/stores/api.svelte';
	import { type CPUPin, type CreateData } from '$lib/types/vm/vm';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { getBasicSettings } from '$lib/api/basic';
	import { resource } from 'runed';
	import { untrack } from 'svelte';

	interface Props {
		open: boolean;
		minimize: boolean;
	}

	let { open = $bindable(), minimize = $bindable() }: Props = $props();

	const networkObjects = resource(
		() => 'network-objects',
		async (key, prevKey, { signal }) => {
			const result = await getNetworkObjects();
			updateCache(key, result);
			return result;
		}
	);

	const networkSwitches = resource(
		() => 'network-switches',
		async (key, prevKey, { signal }) => {
			const result = await getSwitches();
			updateCache(key, result);
			return result;
		}
	);

	const pciDevices = resource(
		() => 'pci-devices',
		async (key, prevKey, { signal }) => {
			const result = await getPCIDevices();
			updateCache(key, result);
			return result;
		}
	);

	const pptDevices = resource(
		() => 'ppt-devices',
		async (key, prevKey, { signal }) => {
			const result = await getPPTDevices();
			updateCache(key, result);
			return result;
		}
	);

	const downloadsByUtype = resource(
		() => 'downloads-by-utype',
		async (key, prevKey, { signal }) => {
			const result = await getDownloadsByUType();
			updateCache(key, result);
			return result;
		}
	);

	const vms = resource(
		() => 'vm-list',
		async (key, prevKey, { signal }) => {
			const result = await getVMs();
			updateCache(key, result);
			return result;
		}
	);

	const jails = resource(
		() => 'simple-jail-list',
		async (key, prevKey, { signal }) => {
			const result = await getJails();
			updateCache(key, result);
			return result;
		}
	);

	const clusterNodes = resource(
		() => 'cluster-nodes',
		async (key, prevKey, { signal }) => {
			const result = await getNodes();
			updateCache(key, result);
			return result;
		}
	);

	const basicSettings = resource(
		() => 'basic-settings',
		async (key, prevKey, { signal }) => {
			const result = await getBasicSettings();
			updateCache(key, result);
			return result;
		}
	);

	let reload = $state(false);

	$effect(() => {
		if (reload || minimize === false) {
			untrack(() => {
				networkObjects.refetch();
				networkSwitches.refetch();
				pciDevices.refetch();
				pptDevices.refetch();
				downloadsByUtype.refetch();
				vms.refetch();
				jails.refetch();
				clusterNodes.refetch();
				basicSettings.refetch();
			});

			reload = false;
		}
	});

	let passablePci: PCIDevice[] = $derived.by(() => {
		if (!pciDevices.current) return [];
		return pciDevices.current.filter((device) => device.name.startsWith('ppt'));
	});

	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let options = {
		name: '',
		id: 0,
		description: '',
		node: '',
		storage: {
			type: 'zvol',
			pool: '',
			size: 1000 * 1000 * 1000,
			emulation: 'ahci-hd',
			iso: ''
		},
		network: {
			switch: 'None',
			mac: '',
			emulation: 'e1000'
		},
		hardware: {
			sockets: 1,
			cores: 1,
			threads: 1,
			memory: 1000 * 1000 * 1000,
			passthroughIds: [] as number[],
			pinnedCPUs: [] as CPUPin[],
			isPinningOpen: false
		},
		advanced: {
			serial: false,
			vncPort: 0,
			vncPassword: generatePassword(),
			vncWait: false,
			vncResolution: '1024x768',
			startAtBoot: false,
			bootOrder: 0,
			tpmEmulation: false,
			timeOffset: 'utc' as 'utc' | 'localtime',
			cloudInit: {
				enabled: false,
				data: '',
				metadata: '',
				networkConfig: ''
			},
			ignoreUmsrs: false
		}
	};

	let nextId = $derived(getNextId(vms.current || [], jails.current || []));
	let modal: CreateData = $state(options);
	let loading = $state(false);
	let lastTab = $state('basic');

	$effect(() => {
		modal.id = nextId;
	});

	async function create() {
		const data = $state.snapshot(modal);
		if (isValidCreateData(data, downloadsByUtype.current || [])) {
			loading = true;
			const response = await newVM(data);
			loading = false;
			if (response.status === 'success') {
				toast.success(`Created VM ${modal.name}`, {
					duration: 3000,
					position: 'bottom-center'
				});
				open = false;
			} else {
				handleAPIError(response);
				toast.error('Failed to create VM', {
					duration: 3000,
					position: 'bottom-center'
				});
			}

			reloadStore.leftPanel = true;
		}
	}

	function resetModal() {
		modal = options;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0  overflow-auto p-5 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-2xl"
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--monitor-outline-rounded] h-5 w-5"></span>
					<span class="cursor-events-none cursor-default">Create Virtual Machine</span>
				</div>
				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" class="h-4" onclick={() => resetModal()} title={'Reset'}>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => {
							minimize = true;
							open = false;
						}}
						title={'Minimize'}
					>
						<span class="icon-[mdi--window-minimize] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Minimize'}</span>
					</Button>

					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => {
							open = false;
							minimize = false;
							lastTab = 'basic';
							resetModal();
						}}
						title={'Close'}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-6 flex-1 overflow-y-auto">
			<Tabs.Root value={lastTab} class="w-full overflow-hidden">
				<Tabs.List class="grid w-full grid-cols-5 p-0 ">
					{#each tabs as { value, label }}
						<Tabs.Trigger class="border-b" {value} onclick={() => (lastTab = value)}
							>{label}</Tabs.Trigger
						>
					{/each}
				</Tabs.List>

				{#each tabs as { value, label }}
					<Tabs.Content {value}>
						<div>
							{#if value === 'basic' && clusterNodes.current}
								<Basic
									bind:name={modal.name}
									bind:node={modal.node}
									bind:id={modal.id}
									bind:description={modal.description}
									nodes={clusterNodes.current}
									bind:refetch={reload}
								/>
							{:else if value === 'storage' && downloadsByUtype.current && basicSettings.current}
								<Storage
									downloads={downloadsByUtype.current}
									pools={basicSettings.current.pools}
									bind:type={modal.storage.type}
									bind:pool={modal.storage.pool}
									bind:size={modal.storage.size}
									bind:emulation={modal.storage.emulation}
									bind:iso={modal.storage.iso}
									cloudInit={modal.advanced.cloudInit}
								/>
							{:else if value === 'network' && networkObjects.current && networkSwitches.current && vms.current}
								<Network
									switches={networkSwitches.current}
									vms={vms.current}
									networkObjects={networkObjects.current}
									bind:switch={modal.network.switch}
									bind:mac={modal.network.mac}
									bind:emulation={modal.network.emulation}
								/>
							{:else if value === 'hardware' && pptDevices.current && vms.current}
								<Hardware
									devices={passablePci}
									vms={vms.current}
									pptDevices={pptDevices.current}
									bind:isPinningOpen={modal.hardware.isPinningOpen}
									bind:sockets={modal.hardware.sockets}
									bind:cores={modal.hardware.cores}
									bind:threads={modal.hardware.threads}
									bind:memory={modal.hardware.memory}
									bind:passthroughIds={modal.hardware.passthroughIds}
									bind:pinnedCPUs={modal.hardware.pinnedCPUs}
								/>
							{:else if value === 'advanced'}
								<Advanced
									bind:serial={modal.advanced.serial}
									bind:vncPort={modal.advanced.vncPort}
									bind:vncPassword={modal.advanced.vncPassword}
									bind:vncWait={modal.advanced.vncWait}
									bind:startAtBoot={modal.advanced.startAtBoot}
									bind:bootOrder={modal.advanced.bootOrder}
									bind:vncResolution={modal.advanced.vncResolution}
									bind:tpmEmulation={modal.advanced.tpmEmulation}
									bind:timeOffset={modal.advanced.timeOffset}
									bind:cloudInit={modal.advanced.cloudInit}
									bind:ignoreUmsrs={modal.advanced.ignoreUmsrs}
								/>
							{/if}
						</div>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end md:flex-row">
				<Button size="sm" type="button" class="h-8" onclick={() => create()} disabled={loading}>
					{#if loading}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create Virtual Machine
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

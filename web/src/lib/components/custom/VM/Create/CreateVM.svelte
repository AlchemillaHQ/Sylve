<script lang="ts">
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getSwitches } from '$lib/api/network/switch';
	import { getPCIDevices, getPPTDevices } from '$lib/api/system/pci';
	import { getDownloadsByUTypeResult } from '$lib/api/utilities/downloader';
	import { getSimpleVMs, newVM } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import type { PCIDevice, PPTDevice } from '$lib/types/system/pci';
	import type { UTypeGroupedDownload } from '$lib/types/utilities/downloader';
	import { buildPassablePCI } from '$lib/utils/system/pci';
	import { generatePassword } from '$lib/utils/string';
	import {
		getNextGuestId,
		getNextId,
		getVMCreateErrorMessage,
		isValidCreateData
	} from '$lib/utils/vm/vm';
	import Advanced from './Advanced.svelte';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { reload as reloadStore } from '$lib/stores/api.svelte';
	import { getBasicSettings } from '$lib/api/system/settings';
	import type { BasicSettings } from '$lib/types/system/settings';
	import { type CPUPin, type CreateData, type VMBootRom } from '$lib/types/vm/vm';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
	import { toast } from 'svelte-sonner';
	import { resource, watch } from 'runed';
	import { fade } from 'svelte/transition';
	import { type NetworkObject } from '$lib/types/network/object';
	import { emptySwitchList, isSwitchList } from '$lib/types/network/switch';
	import { isDemoMode } from '$lib/demo/runtime';
	import { demoVMProfiles, getDemoVMProfile, type DemoVMProfile } from '$lib/demo/vm-profiles';
	import DemoProfiles from '$lib/components/custom/VM/Create/DemoProfiles.svelte';

	interface Props {
		open: boolean;
		minimize: boolean;
	}

	type CreateBootRom = Exclude<VMBootRom, 'uboot'>;
	type CreateVMFormData = Omit<CreateData, 'advanced'> & {
		advanced: Omit<CreateData['advanced'], 'bootRom'> & { bootRom: CreateBootRom };
	};

	let { open = $bindable(), minimize = $bindable() }: Props = $props();

	const defaultDemoProfile = demoVMProfiles[0];
	// @wc-ignore
	let options: CreateVMFormData = {
		name: '',
		id: 0,
		description: isDemoMode ? defaultDemoProfile.description : '',
		node: String(page.params.node || storage.localHostname || storage.hostname || ''),
		storage: {
			type: 'zvol',
			pool: isDemoMode ? 'atlas' : '',
			size: isDemoMode ? defaultDemoProfile.diskBytes : 1024 * 1024 * 1024,
			emulation: isDemoMode ? 'ahci-hd' : 'nvme',
			iso: isDemoMode ? defaultDemoProfile.media.uuid : ''
		},
		network: {
			switch: isDemoMode ? 'lan' : 'None',
			mac: '',
			emulation: 'e1000'
		},
		hardware: {
			sockets: 1,
			cores: 1,
			threads: 1,
			memory: isDemoMode ? defaultDemoProfile.memoryBytes : 1024 * 1024 * 1024,
			passthroughIds: [] as number[],
			pinnedCPUs: [] as CPUPin[],
			isPinningOpen: false
		},
		advanced: {
			vncEnabled: true,
			serial: isDemoMode,
			vncPort: isDemoMode ? Math.floor(Math.random() * (5999 - 5900 + 1)) + 5900 : 0,
			vncBind: '127.0.0.1',
			vncPassword: generatePassword(),
			vncWait: false,
			vncResolution: '640x480',
			startAtBoot: false,
			bootOrder: 0,
			tpmEmulation: false,
			timeOffset: 'utc' as 'utc' | 'localtime',
			bootRom: isDemoMode ? 'none' : 'uefi',
			cloudInit: {
				enabled: false,
				data: '',
				metadata: '',
				networkConfig: ''
			},
			extraBhyveOptionsEnabled: false,
			extraBhyveOptions: '',
			ignoreUmsrs: false,
			qemuGuestAgent: false
		}
	};

	let modal: CreateVMFormData = $state(options);
	let demoProfileId = $state(defaultDemoProfile.id);
	const emptyBasicSettings: BasicSettings = { pools: [], services: [], initialized: false };
	let lastGoodNetworkSwitches = emptySwitchList();

	const networkObjects = resource(
		() => `network-objects-${modal.node || '__default__'}`,
		async (key) => {
			const result = await getNetworkObjects(modal.node || undefined);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [] as NetworkObject[];
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const networkSwitches = resource(
		() => `network-switches-${modal.node || '__default__'}`,
		async (key) => {
			const result = await getSwitches(modal.node || undefined);
			if (!isSwitchList(result)) {
				handleAPIError(result);
				return lastGoodNetworkSwitches;
			}

			lastGoodNetworkSwitches = result;
			updateCache(key, result);
			return result;
		},
		{ initialValue: lastGoodNetworkSwitches }
	);

	const pciDevices = resource(
		() => `pci-devices-${modal.node || '__default__'}`,
		async (key, _previousKey, { data: previousDevices }): Promise<PCIDevice[]> => {
			const result = await getPCIDevices(modal.node || undefined);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return previousDevices ?? [];
			}
			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const pptDevices = resource(
		() => `ppt-devices-${modal.node || '__default__'}`,
		async (key, _previousKey, { data: previousDevices }): Promise<PPTDevice[]> => {
			const result = await getPPTDevices(modal.node || undefined);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return previousDevices ?? [];
			}
			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const lastGoodDownloadsByNode: Record<string, UTypeGroupedDownload[]> = Object.create(null);
	const downloadsByUtype = resource(
		() => modal.node || '__default__',
		async (node, _previousNode, { signal }) => {
			const hostname = node === '__default__' ? undefined : node;
			try {
				const result = await getDownloadsByUTypeResult({ hostname, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastGoodDownloadsByNode[node] ?? [];
				}
				lastGoodDownloadsByNode[node] = result;
				await updateCache('downloads-by-utype', result, hostname);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastGoodDownloadsByNode[node] ?? [];
				throw error;
			}
		},
		{ initialValue: [] }
	);

	const vms = resource(
		() => `simple-vm-list-${modal.node || '__default__'}`,
		async (key) => {
			const result = await getSimpleVMs(modal.node || undefined);
			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const jails = resource(
		() => `simple-jail-list-${modal.node || '__default__'}`,
		async (key) => {
			const result = await getSimpleJails(modal.node || undefined);
			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const clusterNodes = resource(
		() => 'cluster-nodes',
		async (key) => {
			const result = await getNodes();
			updateCache(key, result);
			return result;
		},
		{ initialValue: [] }
	);

	const basicSettings = resource(
		() => `basic-settings-${modal.node || '__default__'}`,
		async (key, _previousKey, { data: previousSettings }) => {
			const result = await getBasicSettings(modal.node || undefined);
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return previousSettings ?? emptyBasicSettings;
			}

			updateCache(key, result);
			return result;
		},
		{ initialValue: emptyBasicSettings }
	);

	watch([() => open, () => minimize], ([open, minimize]) => {
		if (open && !minimize) {
			networkObjects.refetch();
			networkSwitches.refetch();
			pciDevices.refetch();
			pptDevices.refetch();
			downloadsByUtype.refetch();
			vms.refetch();
			jails.refetch();
			clusterNodes.refetch();
			basicSettings.refetch();
		}
	});

	watch(
		() => modal.node,
		(node) => {
			if (!node || node.trim() === '') return;
			modal.storage.pool = '';
			modal.storage.iso = '';
			modal.network.switch = 'None';
			modal.network.mac = '';
			modal.hardware.passthroughIds = [];
			modal.hardware.pinnedCPUs = [];
		}
	);

	let passablePci: PCIDevice[] = $derived.by(() => {
		if (!pciDevices.current || !pptDevices.current) return [];
		return buildPassablePCI(pciDevices.current, pptDevices.current).map(({ device }) => device);
	});

	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let nextId = $derived.by(() => {
		if (
			clusterNodes.current &&
			Array.isArray(clusterNodes.current) &&
			clusterNodes.current.length > 0
		) {
			return getNextGuestId(clusterNodes.current);
		}

		return getNextId(vms.current || [], jails.current || []);
	});

	let loading = $state(false);
	let lastTab = $state('basic');

	function selectDemoProfile(profile: DemoVMProfile) {
		demoProfileId = profile.id;
		modal.name = `${profile.defaultName}-${modal.id || 'vm'}`;
		modal.description = profile.description;
		modal.storage.type = 'zvol';
		modal.storage.pool = 'atlas';
		modal.storage.size = profile.diskBytes;
		modal.storage.emulation = 'ahci-hd';
		modal.storage.iso = profile.media.uuid;
		modal.network.switch = 'lan';
		modal.network.emulation = 'e1000';
		modal.hardware.sockets = 1;
		modal.hardware.cores = 1;
		modal.hardware.threads = 1;
		modal.hardware.memory = profile.memoryBytes;
		modal.hardware.passthroughIds = [];
		modal.hardware.pinnedCPUs = [];
		modal.advanced.serial = true;
		modal.advanced.vncEnabled = true;
		modal.advanced.bootRom = 'none';
		modal.advanced.cloudInit.enabled = false;
	}

	watch(
		() => nextId,
		(nextId) => {
			if (typeof nextId === 'number') {
				modal.id = nextId;
				if (isDemoMode) {
					const profile = getDemoVMProfile(demoProfileId) ?? defaultDemoProfile;
					const generatedNames = demoVMProfiles.map((entry) => entry.defaultName);
					if (
						modal.name === '' ||
						generatedNames.some(
							(prefix) => modal.name === prefix || modal.name.startsWith(`${prefix}-`)
						)
					) {
						modal.name = `${profile.defaultName}-${nextId}`;
					}
				}
			}
		}
	);

	async function create() {
		const data: CreateData = $state.snapshot(modal);
		if (!isValidCreateData(data, downloadsByUtype.current || [])) return;

		loading = true;
		try {
			const response = await newVM(data, data.node);
			if (response.status === 'success') {
				toast.success(`Created VM ${data.name}`, {
					duration: 3000,
					position: 'bottom-center'
				});
				open = false;
				reloadStore.leftPanel = true;
			} else {
				handleAPIError(response);
				toast.error(getVMCreateErrorMessage(response), {
					duration: 3000,
					position: 'bottom-center'
				});
			}
		} finally {
			loading = false;
		}
	}

	function resetModal() {
		modal = options;
		if (isDemoMode) {
			demoProfileId = defaultDemoProfile.id;
			selectDemoProfile(defaultDemoProfile);
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[90%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0 overflow-auto p-6 transition-all duration-300 ease-in-out lg:h-[72vh] {isDemoMode
			? 'lg:max-w-[900px]'
			: 'lg:max-w-[720px]'}"
		showCloseButton={false}
	>
		<Dialog.Header>
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[material-symbols--monitor-outline-rounded] h-5 w-5"></span>
					<span class="cursor-events-none cursor-default">Create Virtual Machine</span>
				</div>
				<div class="flex items-center gap-0.5 -mr-3">
					<Button size="sm" variant="link" class="h-4" onclick={() => resetModal()} title="Reset">
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Reset</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						onclick={() => {
							minimize = true;
							open = false;
						}}
						title="Minimize"
					>
						<span class="icon-[mdi--window-minimize] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Minimize</span>
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
						title="Close"
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-6 flex-1 overflow-y-auto">
			{#if isDemoMode}
				<div class="space-y-5">
					<div>
						<p class="text-sm font-medium">Browser-compatible image</p>
						<p class="text-muted-foreground mt-1 text-xs leading-5">
							Demo VMs use a single CPU and a prepared 32-bit image so their console can run locally
							in the browser.
						</p>
					</div>
					<DemoProfiles bind:value={demoProfileId} onSelect={selectDemoProfile} />
					<div class="rounded-lg border">
						<Basic
							bind:name={modal.name}
							bind:node={modal.node}
							bind:id={modal.id}
							bind:description={modal.description}
							nodes={clusterNodes.current}
						/>
					</div>
				</div>
			{:else}
				<Tabs.Root value={lastTab} class="w-full overflow-hidden">
					<Tabs.List class="grid w-full grid-cols-5 p-0 ">
						{#each tabs as { value, label } (value)}
							<Tabs.Trigger class="border-b" {value} onclick={() => (lastTab = value)}
								>{label}</Tabs.Trigger
							>
						{/each}
					</Tabs.List>

					{#each tabs as { value } (value)}
						<Tabs.Content {value}>
							<div>
								{#if value === 'basic'}
									<div in:fade={{ duration: 200 }}>
										<Basic
											bind:name={modal.name}
											bind:node={modal.node}
											bind:id={modal.id}
											bind:description={modal.description}
											nodes={clusterNodes.current}
										/>
									</div>
								{:else if value === 'storage'}
									<div in:fade={{ duration: 200 }}>
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
									</div>
								{:else if value === 'network' && networkSwitches.current && Array.isArray(networkObjects.current)}
									<div in:fade={{ duration: 200 }}>
										<Network
											switches={networkSwitches.current}
											networkObjects={networkObjects.current}
											bind:switch={modal.network.switch}
											bind:mac={modal.network.mac}
											bind:emulation={modal.network.emulation}
										/>
									</div>
								{:else if value === 'hardware'}
									<div in:fade={{ duration: 200 }}>
										<Hardware
											node={modal.node}
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
									</div>
								{:else if value === 'advanced'}
									<div in:fade={{ duration: 200 }}>
										<Advanced
											node={modal.node}
											bind:vncEnabled={modal.advanced.vncEnabled}
											bind:serial={modal.advanced.serial}
											bind:vncPort={modal.advanced.vncPort}
											bind:vncBind={modal.advanced.vncBind}
											bind:vncPassword={modal.advanced.vncPassword}
											bind:vncWait={modal.advanced.vncWait}
											bind:startAtBoot={modal.advanced.startAtBoot}
											bind:bootOrder={modal.advanced.bootOrder}
											bind:vncResolution={modal.advanced.vncResolution}
											bind:tpmEmulation={modal.advanced.tpmEmulation}
											bind:timeOffset={modal.advanced.timeOffset}
											bind:bootRom={modal.advanced.bootRom}
											bind:cloudInit={modal.advanced.cloudInit}
											bind:extraBhyveOptionsEnabled={modal.advanced.extraBhyveOptionsEnabled}
											bind:extraBhyveOptions={modal.advanced.extraBhyveOptions}
											bind:ignoreUmsrs={modal.advanced.ignoreUmsrs}
											bind:qemuGuestAgent={modal.advanced.qemuGuestAgent}
										/>
									</div>
								{/if}
							</div>
						</Tabs.Content>
					{/each}
				</Tabs.Root>
			{/if}
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

<script lang="ts">
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getSimpleJails, newJail } from '$lib/api/jail/jail';
	import { getBootstraps } from '$lib/api/jail/bootstrap';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { getDownloads } from '$lib/api/utilities/downloader';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { CreateData } from '$lib/types/jail/jail';
	import { handleAPIError, updateCache } from '$lib/utils/http';
	import { getJailCreateErrorMessage, isValidCreateData } from '$lib/utils/jail/jail';
	import { getNextGuestId, getNextId } from '$lib/utils/vm/vm';
	import { fade } from 'svelte/transition';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import Basic from './Basic.svelte';
	import Hardware from './Hardware.svelte';
	import Advanced from './Advanced.svelte';
	import Network from './Network.svelte';
	import Storage from './Storage.svelte';
	import { getPools } from '$lib/api/zfs/pool';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';

	interface Props {
		open: boolean;
		minimize: boolean;
	}

	let { open = $bindable(), minimize = $bindable() }: Props = $props();
	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	let downloads = resource(
		() => 'downloads',
		async (key) => {
			const downloads = await getDownloads();
			updateCache(key, downloads);
			return downloads;
		}
	);

	let networkSwitches = resource(
		() => 'network-switches',
		async (key) => {
			const switches = await getSwitches();
			updateCache(key, switches);
			return switches;
		}
	);

	let networkObjects = resource(
		() => 'network-objects',
		async (key) => {
			const objects = await getNetworkObjects();
			updateCache(key, objects);
			return objects;
		}
	);

	let networkRefetch = $state(false);

	watch(
		() => networkRefetch,
		(value) => {
			if (value) {
				networkObjects.refetch();
				networkRefetch = false;
			}
		}
	);

	let vms = resource(
		() => 'simple-vm-list',
		async (key) => {
			const vms = await getSimpleVMs();
			updateCache(key, vms);
			return vms;
		}
	);

	let jails = resource(
		() => 'simple-jail-list',
		async (key) => {
			const jails = await getSimpleJails();
			updateCache(key, jails);
			return jails;
		}
	);

	let nodes = resource(
		() => 'cluster-nodes',
		async (key) => {
			const nodes = await getNodes();
			updateCache(key, nodes);
			return nodes;
		}
	);

	let pools = resource(
		() => 'pool-list',
		async (key) => {
			const pools = await getPools();
			updateCache(key, pools);
			return pools;
		}
	);

	const clusterNodes = resource(
		() => 'cluster-nodes',
		async (key) => {
			const result = await getNodes();
			updateCache(key, result);
			return result;
		}
	);

	let bootstrapRefetch = $state(false);

	let bootstraps = resource(
		() => (open && modal.storage.pool ? `bootstraps-${modal.storage.pool}` : null),
		async (key) => {
			if (!modal.storage.pool) return [] as BootstrapEntry[];
			const result = await getBootstraps(modal.storage.pool);
			if (key !== null) {
				updateCache(key, result);
			}
			return result;
		},
		{ initialValue: [] as BootstrapEntry[] }
	);

	watch(
		() => bootstrapRefetch,
		(value) => {
			if (value) {
				bootstraps.refetch();
				bootstrapRefetch = false;
			}
		}
	);

	let refetch = $state(false);

	watch(
		() => refetch,
		(value) => {
			if (value) {
				downloads.refetch();
				networkSwitches.refetch();
				networkObjects.refetch();
				vms.refetch();
				jails.refetch();
				nodes.refetch();
				pools.refetch();
				bootstraps.refetch();

				refetch = false;
			}
		}
	);

	let creating: boolean = $state(false);

	let options = {
		name: '',
		hostname: '',
		id: 0,
		node: '',
		description: '',
		storage: {
			pool: '',
			base: '',
			bootstrapName: '',
			fstab: ''
		},
		network: {
			switch: 'None',
			mac: 0,
			inheritIPv4: true,
			inheritIPv6: true,
			ipv4: 0,
			ipv4Gateway: 0,
			ipv6: 0,
			ipv6Gateway: 0,
			dhcp: false,
			slaac: false,
			resolvConf: ''
		},
		hardware: {
			cpuCores: 1,
			ram: 0,
			startAtBoot: false,
			resourceLimits: true,
			bootOrder: 0,
			devfsRuleset: ''
		},
		advanced: {
			jailType: 'freebsd' as 'linux' | 'freebsd',
			additionalOptions: '',
			cleanEnvironment: true,
			execScripts: {
				prestart: { enabled: false, script: '' },
				start: { enabled: false, script: '' },
				poststart: { enabled: false, script: '' },
				prestop: { enabled: false, script: '' },
				stop: { enabled: false, script: '' },
				poststop: { enabled: false, script: '' }
			},
			allowedOptions: [] as string[],
			metadata: {
				env: '',
				meta: ''
			}
		}
	};

	let nextId = $derived.by(() => {
		if (open) {
			if (
				clusterNodes.current &&
				Array.isArray(clusterNodes.current) &&
				clusterNodes.current.length > 0
			) {
				return getNextGuestId(clusterNodes.current);
			}

			return getNextId(vms.current || [], jails.current || []);
		}
	});

	let modal: CreateData = $state(options);

	watch(
		() => nextId,
		(id) => {
			if (typeof id === 'number') {
				modal.id = id;
			}
		}
	);

	function resetModal() {
		modal = options;
	}

	async function create() {
		const data = $state.snapshot(modal);

		if (data.hardware.resourceLimits === false) {
			data.hardware.cpuCores = 0;
			data.hardware.ram = 0;
		}

		// Detect bootstrap: prefix and route accordingly
		if (data.storage.base.startsWith('bootstrap:')) {
			data.storage.bootstrapName = data.storage.base.slice('bootstrap:'.length);
			data.storage.base = '';
		} else {
			data.storage.bootstrapName = '';
		}

		if (!(await isValidCreateData(data))) {
			return;
		} else {
			creating = true;
			const response = await newJail(data);
			creating = false;

			if (response.error) {
				handleAPIError(response);

				reload.leftPanel = true;
				toast.error(getJailCreateErrorMessage(response), {
					position: 'bottom-center'
				});
				return;
			}

			open = false;
			reload.leftPanel = true;

			toast.success(`Jail ${data.name} created`, {
				position: 'bottom-center'
			});
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0  overflow-auto p-5 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-2xl"
		showCloseButton={false}
	>
		<Dialog.Header class="p-0">
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[hugeicons--prison] h-5 w-5"></span>
					<span>Create Jail</span>
				</div>
				<div class="flex items-center gap-0.5">
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
					<Button size="sm" variant="link" class="h-4" onclick={() => (open = false)} title="Close">
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-6 flex-1 overflow-y-auto">
			<Tabs.Root value="basic" class="w-full overflow-hidden">
				<Tabs.List class="grid w-full grid-cols-5 p-0">
					{#each tabs as { value, label } (value)}
						<Tabs.Trigger class="border-b" {value}>{label}</Tabs.Trigger>
					{/each}
				</Tabs.List>

				{#each tabs as { value } (value)}
					<Tabs.Content {value}>
						<div>
							{#if value === 'basic' && nodes.current}
								<div in:fade={{ duration: 200 }}>
									<Basic
										bind:name={modal.name}
										bind:id={modal.id}
										bind:hostname={modal.hostname}
										bind:description={modal.description}
										bind:refetch
										bind:node={modal.node}
										nodes={nodes.current}
									/>
								</div>
							{:else if value === 'storage' && pools.current && downloads.current && jails.current}
								<div in:fade={{ duration: 200 }}>
									<Storage
										downloads={downloads.current}
										pools={pools.current}
										bootstraps={bootstraps.current}
										bind:bootstrapRefetch
										ctId={modal.id}
										bind:pool={modal.storage.pool}
										bind:base={modal.storage.base}
										bind:fstab={modal.storage.fstab}
									/>
								</div>
							{:else if value === 'network' && networkSwitches.current && networkObjects.current}
								<div in:fade={{ duration: 200 }}>
									<Network
										name={modal.name}
										ctId={modal.id}
										bind:switch={modal.network.switch}
										bind:mac={modal.network.mac}
										bind:inheritIPv4={modal.network.inheritIPv4}
										bind:inheritIPv6={modal.network.inheritIPv6}
										bind:ipv4={modal.network.ipv4}
										bind:ipv4Gateway={modal.network.ipv4Gateway}
										bind:ipv6={modal.network.ipv6}
										bind:ipv6Gateway={modal.network.ipv6Gateway}
										bind:dhcp={modal.network.dhcp}
										bind:slaac={modal.network.slaac}
										bind:resolvConf={modal.network.resolvConf}
										bind:refetch={networkRefetch}
										jailType={modal.advanced.jailType}
										switches={networkSwitches.current}
										networkObjects={networkObjects.current as NetworkObject[]}
									/>
								</div>
							{:else if value === 'hardware'}
								<div in:fade={{ duration: 200 }}>
									<Hardware
										bind:cpuCores={modal.hardware.cpuCores}
										bind:ram={modal.hardware.ram}
										bind:startAtBoot={modal.hardware.startAtBoot}
										bind:bootOrder={modal.hardware.bootOrder}
										bind:resourceLimits={modal.hardware.resourceLimits}
										bind:devfsRuleset={modal.hardware.devfsRuleset}
									/>
								</div>
							{:else if value === 'advanced'}
								<div in:fade={{ duration: 200 }}>
									<Advanced
										bind:jailType={modal.advanced.jailType}
										bind:additionalOptions={modal.advanced.additionalOptions}
										bind:cleanEnvironment={modal.advanced.cleanEnvironment}
										bind:execScripts={modal.advanced.execScripts}
										bind:allowedOptions={modal.advanced.allowedOptions}
										bind:metadata={modal.advanced.metadata}
									/>
								</div>
							{/if}
						</div>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end md:flex-row">
				<Button size="sm" type="button" class="h-8" onclick={() => create()} disabled={creating}>
					<!-- Create Jail -->
					{#if creating}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else}
						Create Jail
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

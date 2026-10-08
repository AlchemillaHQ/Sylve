<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { storage } from '$lib';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getSimpleJails, getJailZFSSources, newJail, validateNewJail } from '$lib/api/jail/jail';
	import { getLifecycleTask } from '$lib/api/task/lifecycle';
	import { getBootstraps } from '$lib/api/jail/bootstrap';
	import { getNetworkObjects } from '$lib/api/network/object';
	import { getSwitches } from '$lib/api/network/switch';
	import { getDownloadsResult } from '$lib/api/utilities/downloader';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { getDatasetsResult } from '$lib/api/zfs/datasets';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import type { CreateData } from '$lib/types/jail/jail';
	import { JailCreationAcceptedSchema } from '$lib/types/jail/jail';
	import { LifecycleTaskSchema, isLifecycleTaskActive } from '$lib/types/task/lifecycle';
	import type { Download } from '$lib/types/utilities/downloader';
	import { GZFSDatasetTypeSchema, type Dataset } from '$lib/types/zfs/dataset';
	import {
		handleAPIError,
		isAPIResponse,
		isRequestCancellation,
		updateCache
	} from '$lib/utils/http';
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
	import CreationProgress from './CreationProgress.svelte';
	import { getPools } from '$lib/api/zfs/pool';
	import type { NetworkObject } from '$lib/types/network/object';
	import type { BootstrapEntry } from '$lib/types/jail/bootstrap';
	import { emptySwitchList, isSwitchList } from '$lib/types/network/switch';

	interface Props {
		open: boolean;
		minimize: boolean;
		devFSDisabled?: boolean;
		taskId?: number;
		taskHostname?: string;
	}

	let {
		open = $bindable(),
		minimize = $bindable(),
		devFSDisabled = false,
		taskId = $bindable(0),
		taskHostname = $bindable('')
	}: Props = $props();
	const tabs = [
		{ value: 'basic', label: 'Basic' },
		{ value: 'storage', label: 'Storage' },
		{ value: 'network', label: 'Network' },
		{ value: 'hardware', label: 'Hardware' },
		{ value: 'advanced', label: 'Advanced' }
	];

	// @wc-ignore
	let options = {
		name: '',
		hostname: '',
		id: 0,
		node: String(page.params.node || storage.localHostname || storage.hostname || ''),
		description: '',
		storage: {
			pool: '',
			base: '',
			bootstrapName: '',
			source: 'base' as 'base' | 'zfs',
			zfsSource: { dataset: '', guid: '' },
			fstab: ''
		},
		network: {
			switch: 'None',
			mac: 0,
			macRaw: '',
			inheritIPv4: true,
			inheritIPv6: true,
			ipv4: 0,
			ipv4Raw: '',
			ipv4Gateway: 0,
			ipv4GatewayRaw: '',
			ipv6: 0,
			ipv6Raw: '',
			ipv6Gateway: 0,
			ipv6GatewayRaw: '',
			dhcp: false,
			slaac: false,
			resolvConf: '',
			vlanFiltering: false,
			vlanPolicyMode: '' as '' | 'access' | 'trunk',
			vlanPolicyUntagged: '',
			vlanPolicyTagged: ''
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

	let modal: CreateData = $state(structuredClone(options));
	let submitError = $state('');
	let notifiedTask = $state(0);
	let previousTask = $state<{ id: number; hostname: string } | null>(null);
	let followedTask: string | null = null;
	let taskKey = $derived(taskId ? `${taskHostname}::${taskId}` : null);
	let creationTask = resource(
		[() => (open ? taskId : 0), () => taskHostname],
		async (
			[id, hostname],
			_previous,
			{ signal, data }
		): Promise<{ key: string; result: Awaited<ReturnType<typeof getLifecycleTask>> } | null> => {
			// Keep the content stable while the dialog's exit animation finishes.
			if (!id) return data ?? null;
			return {
				key: `${hostname}::${id}`,
				result: await getLifecycleTask(id, hostname || undefined, signal)
			};
		}
	);
	let observedResult = $derived(
		creationTask.current?.key === taskKey ? creationTask.current?.result : null
	);
	let task = $derived(observedResult && !isAPIResponse(observedResult) ? observedResult : null);
	$effect(() => {
		if (!open || !taskId || (task && !isLifecycleTaskActive(task))) return;
		const interval = setInterval(() => creationTask.refetch(), 2000);
		return () => clearInterval(interval);
	});
	watch(
		() => task,
		(current) => {
			if (current && isLifecycleTaskActive(current)) followedTask = taskKey;
			if (current && !isLifecycleTaskActive(current) && current.id !== notifiedTask) {
				notifiedTask = current.id;
				reload.leftPanel = true;
				reload.auditLog = true;
			}
			if (current?.status === 'success' && followedTask === taskKey) {
				open = false;
				minimize = false;
			}
		}
	);

	function observeTask(id: number, hostname: string) {
		notifiedTask = 0;
		taskHostname = hostname;
		taskId = id;
		submitError = '';
	}
	let lastGoodNetworkSwitches = emptySwitchList();
	const lastGoodDownloadsByNode: Record<string, Download[]> = Object.create(null);
	const lastGoodFilesystemsByNode: Record<string, Dataset[]> = Object.create(null);

	let downloads = resource(
		() => modal.node || '__default__',
		async (node, _previousNode, { signal }) => {
			const hostname = node === '__default__' ? undefined : node;
			try {
				const result = await getDownloadsResult({ hostname, signal });
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return lastGoodDownloadsByNode[node] ?? [];
				}
				lastGoodDownloadsByNode[node] = result;
				await updateCache('download-list', result, hostname);
				return result;
			} catch (error) {
				if (isRequestCancellation(error)) return lastGoodDownloadsByNode[node] ?? [];
				throw error;
			}
		}
	);

	let networkSwitches = resource(
		() => `network-switches-${modal.node || '__default__'}`,
		async (key) => {
			const switches = await getSwitches(modal.node || undefined);
			if (!isSwitchList(switches)) {
				handleAPIError(switches);
				return lastGoodNetworkSwitches;
			}

			lastGoodNetworkSwitches = switches;
			updateCache(key, switches);
			return switches;
		},
		{ initialValue: lastGoodNetworkSwitches }
	);

	let networkObjects = resource(
		() => `network-objects-${modal.node || '__default__'}`,
		async (key) => {
			const objects = await getNetworkObjects(modal.node || undefined);
			if (isAPIResponse(objects)) {
				handleAPIError(objects);
				return [] as NetworkObject[];
			}

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
		() => `simple-vm-list-${modal.node || '__default__'}`,
		async (key) => {
			const vms = await getSimpleVMs(modal.node || undefined);
			updateCache(key, vms);
			return vms;
		}
	);

	let jails = resource(
		() => `simple-jail-list-${modal.node || '__default__'}`,
		async (key) => {
			const jails = await getSimpleJails(modal.node || undefined);
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
		() => `pool-list-${modal.node || '__default__'}`,
		async (key) => {
			const pools = await getPools(false, modal.node || undefined);
			updateCache(key, pools);
			return pools;
		}
	);

	let filesystemData = resource(
		() => modal.node || '__default__',
		async (node, _previousNode, { signal }) => {
			const hostname = node === '__default__' ? '' : node;
			try {
				const result = await getDatasetsResult(
					GZFSDatasetTypeSchema.enum.FILESYSTEM,
					hostname,
					signal
				);
				if (isAPIResponse(result)) {
					handleAPIError(result);
					return { node, datasets: lastGoodFilesystemsByNode[node] ?? [] };
				}
				lastGoodFilesystemsByNode[node] = result;
				await updateCache('zfs-filesystems', result, hostname || undefined);
				return { node, datasets: result };
			} catch (error) {
				if (isRequestCancellation(error))
					return { node, datasets: lastGoodFilesystemsByNode[node] ?? [] };
				throw error;
			}
		},
		{ initialValue: { node: '', datasets: [] as Dataset[] } }
	);
	let filesystems = $derived(
		filesystemData.current.node === (modal.node || '__default__')
			? filesystemData.current.datasets
			: []
	);

	let bootstrapRefetch = $state(false);
	let zfsSourceData = resource(
		() => (open && !taskId && modal.storage.source === 'zfs' ? modal.node : null),
		async (hostname, _previous, { signal }) => {
			if (hostname === null) return null;
			const result = await getJailZFSSources(hostname || undefined, signal);
			if (isAPIResponse(result)) handleAPIError(result);
			return { hostname, result };
		}
	);
	let zfsSourceResult = $derived(
		zfsSourceData.current?.hostname === modal.node ? zfsSourceData.current.result : null
	);
	let sourceDatasets = $derived(Array.isArray(zfsSourceResult) ? zfsSourceResult : []);
	let sourcesError = $derived(
		isAPIResponse(zfsSourceResult) || zfsSourceData.error
			? 'Could not load source datasets. Close and reopen the dialog to retry.'
			: ''
	);

	let bootstraps = resource(
		() =>
			open && modal.storage.pool
				? `${modal.node || '__default__'}:bootstraps-${modal.storage.pool}`
				: null,
		async (key) => {
			if (!modal.storage.pool) return [] as BootstrapEntry[];
			const result = await getBootstraps(modal.storage.pool, {
				hostname: modal.node || undefined
			});
			if (isAPIResponse(result)) {
				handleAPIError(result);
				return [] as BootstrapEntry[];
			}
			if (key !== null) {
				updateCache(`bootstraps-${modal.storage.pool}`, result, modal.node || undefined);
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

	watch([() => open, () => minimize], ([open, minimize]) => {
		if (open && !minimize) {
			downloads.refetch();
			networkSwitches.refetch();
			networkObjects.refetch();
			vms.refetch();
			jails.refetch();
			nodes.refetch();
			pools.refetch();
			filesystemData.refetch();
			zfsSourceData.refetch();
			bootstraps.refetch();
		}
	});

	watch(
		() => modal.node,
		(node) => {
			if (!node || node.trim() === '') return;
			modal.storage.pool = '';
			modal.storage.base = '';
			modal.storage.bootstrapName = '';
			modal.storage.zfsSource = { dataset: '', guid: '' };
			submitError = '';
			modal.storage.fstab = '';
			modal.network.switch = 'None';
			modal.network.mac = 0;
			modal.network.ipv4 = 0;
			modal.network.ipv4Gateway = 0;
			modal.network.ipv6 = 0;
			modal.network.ipv6Gateway = 0;
			modal.network.vlanFiltering = false;
			modal.network.vlanPolicyMode = '';
			modal.network.vlanPolicyUntagged = '';
			modal.network.vlanPolicyTagged = '';
		}
	);

	let creating: boolean = $state(false);

	let nextId = $derived.by(() => {
		if (open) {
			if (nodes.current && Array.isArray(nodes.current) && nodes.current.length > 0) {
				return getNextGuestId(nodes.current);
			}

			return getNextId(vms.current || [], jails.current || []);
		}
	});

	watch(
		() => nextId,
		(id) => {
			if (typeof id === 'number' && !creating && !taskId) {
				modal.id = id;
			}
		}
	);

	function resetModal() {
		if (creating || taskId) return;
		modal = structuredClone(options);
		modal.id = nextId ?? 0;
		submitError = '';
	}

	function newCreation() {
		const hostname = taskHostname || modal.node;
		const returningToForm = previousTask?.id === taskId && previousTask.hostname === taskHostname;
		previousTask = { id: taskId, hostname: taskHostname };
		followedTask = null;
		taskId = 0;
		taskHostname = '';
		if (!returningToForm) {
			resetModal();
			modal.node = hostname;
		}
		jails.refetch();
		vms.refetch();
	}

	async function create() {
		if (creating) return;
		submitError = '';
		const data = $state.snapshot(modal);

		if (data.hardware.resourceLimits === false) {
			data.hardware.cpuCores = 0;
			data.hardware.ram = 0;
		}

		// Detect bootstrap: prefix and route accordingly
		if (data.storage.source === 'zfs') {
			data.storage.base = '';
			data.storage.bootstrapName = '';
		} else if (data.storage.base.startsWith('bootstrap:')) {
			data.storage.bootstrapName = data.storage.base.slice('bootstrap:'.length);
			data.storage.base = '';
		} else {
			data.storage.bootstrapName = '';
		}

		if (!(await isValidCreateData(data))) return;

		creating = true;
		try {
			const validation = await validateNewJail(data, data.node || undefined);
			if (validation.status !== 'success') {
				submitError = getJailCreateErrorMessage(validation);
				handleAPIError(validation);
				return;
			}
			const response = await newJail(data, data.node || undefined);

			if (response.status !== 'success') {
				handleAPIError(response);
				const message = getJailCreateErrorMessage(response);
				submitError = message;
				const existing = LifecycleTaskSchema.safeParse(response.data);
				if (existing.success && existing.data.action === 'create')
					observeTask(existing.data.id, data.node);
				toast.error(message, {
					position: 'bottom-center'
				});
				return;
			}

			const accepted = JailCreationAcceptedSchema.parse(response.data);
			observeTask(accepted.taskId, data.node);
			followedTask = `${data.node}::${accepted.taskId}`;
			reload.leftPanel = true;

			toast.success(`Jail ${data.name} creation queued`, {
				position: 'bottom-center'
			});
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Failed to queue jail creation';
			submitError = message;
			toast.error(message, {
				position: 'bottom-center'
			});
		} finally {
			creating = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class={taskId
			? 'flex max-h-[85vh] flex-col gap-4 overflow-hidden p-6 sm:max-w-lg'
			: 'fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 transform flex-col gap-0 overflow-auto p-6 transition-all duration-300 ease-in-out lg:h-[72vh] lg:max-w-2xl'}
		showCloseButton={false}
		style="--tw-exit-scale: 1"
	>
		<Dialog.Header>
			<Dialog.Title class="flex  justify-between gap-1 text-left">
				<div class="flex items-center gap-2">
					<span class="icon-[hugeicons--prison] h-5 w-5"></span>
					<span>Create Jail</span>
				</div>
				<div class="flex items-center gap-0.5 -mr-3">
					{#if !taskId}
						<Button
							size="sm"
							variant="link"
							class="h-4"
							onclick={() => resetModal()}
							title="Reset"
							disabled={creating}
						>
							<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
							<span class="sr-only">Reset</span>
						</Button>
					{/if}
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
							minimize = taskId > 0 || creating;
							if (!minimize) resetModal();
						}}
						title="Close"
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">Close</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class={taskId ? 'min-h-0 overflow-y-auto' : 'mt-6 flex-1 overflow-y-auto'}>
			{#if taskId}
				{#if task}
					<CreationProgress {task} />
				{:else if observedResult && isAPIResponse(observedResult)}
					<Alert variant="destructive"
						><AlertTitle>Could not load creation task</AlertTitle><AlertDescription
							>Close and reopen the dialog to retry. The task is not cancelled.</AlertDescription
						></Alert
					>
				{:else}<p class="text-muted-foreground p-4 text-sm">Loading creation task…</p>{/if}
			{:else}
				{#if submitError}<Alert variant="destructive" class="mb-3"
						><AlertTitle>Creation not allowed</AlertTitle><AlertDescription
							>{submitError}</AlertDescription
						></Alert
					>{/if}
				<fieldset disabled={creating}>
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
												bind:node={modal.node}
												nodes={nodes.current}
											/>
										</div>
									{:else if value === 'storage' && pools.current && downloads.current && jails.current}
										<div in:fade={{ duration: 200 }}>
											<Storage
												hostname={modal.node || undefined}
												downloads={downloads.current}
												pools={pools.current}
												datasets={filesystems}
												bootstraps={bootstraps.current}
												bind:bootstrapRefetch
												ctId={modal.id}
												bind:pool={modal.storage.pool}
												bind:base={modal.storage.base}
												bind:fstab={modal.storage.fstab}
												bind:source={modal.storage.source}
												bind:zfsSource={modal.storage.zfsSource}
												{sourceDatasets}
												sourcesLoading={zfsSourceData.loading}
												{sourcesError}
											/>
										</div>
									{:else if value === 'network' && networkSwitches.current && Array.isArray(networkObjects.current)}
										<div in:fade={{ duration: 200 }}>
											<Network
												name={modal.name}
												ctId={modal.id}
												bind:switch={modal.network.switch}
												bind:mac={modal.network.mac}
												bind:macRaw={modal.network.macRaw}
												bind:inheritIPv4={modal.network.inheritIPv4}
												bind:inheritIPv6={modal.network.inheritIPv6}
												bind:ipv4={modal.network.ipv4}
												bind:ipv4Raw={modal.network.ipv4Raw}
												bind:ipv4Gateway={modal.network.ipv4Gateway}
												bind:ipv4GatewayRaw={modal.network.ipv4GatewayRaw}
												bind:ipv6={modal.network.ipv6}
												bind:ipv6Raw={modal.network.ipv6Raw}
												bind:ipv6Gateway={modal.network.ipv6Gateway}
												bind:ipv6GatewayRaw={modal.network.ipv6GatewayRaw}
												bind:dhcp={modal.network.dhcp}
												bind:slaac={modal.network.slaac}
												bind:resolvConf={modal.network.resolvConf}
												bind:vlanFiltering={modal.network.vlanFiltering}
												bind:vlanPolicyMode={modal.network.vlanPolicyMode}
												bind:vlanPolicyUntagged={modal.network.vlanPolicyUntagged}
												bind:vlanPolicyTagged={modal.network.vlanPolicyTagged}
												bind:refetch={networkRefetch}
												jailType={modal.advanced.jailType}
												switches={networkSwitches.current}
												networkObjects={networkObjects.current}
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
												{devFSDisabled}
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
												{devFSDisabled}
											/>
										</div>
									{/if}
								</div>
							</Tabs.Content>
						{/each}
					</Tabs.Root>
				</fieldset>
			{/if}
		</div>

		<Dialog.Footer>
			<div class="flex w-full justify-end md:flex-row">
				{#if taskId}
					<Button size="sm" variant="outline" class="h-8" onclick={newCreation}
						>Create Another Jail</Button
					>
					{#if task?.status === 'success'}<Button
							size="sm"
							class="ml-2 h-8"
							disabled={!(taskHostname || storage.localHostname || storage.hostname)}
							onclick={() => {
								goto(
									resolve('/[node]/jail/[ctid]/summary', {
										node: taskHostname || storage.localHostname || storage.hostname || '',
										ctid: String(task!.guestId)
									})
								);
								open = false;
								minimize = false;
							}}>Open Jail</Button
						>{/if}
				{:else}
					{#if previousTask}
						<Button
							size="sm"
							variant="outline"
							class="mr-2 h-8"
							disabled={creating}
							onclick={() => observeTask(previousTask!.id, previousTask!.hostname)}
							>View Progress</Button
						>
					{/if}
					<Button size="sm" type="button" class="h-8" onclick={() => create()} disabled={creating}>
						<!-- Create Jail -->
						{#if creating}
							<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
						{:else}
							Create Jail
						{/if}
					</Button>
				{/if}
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

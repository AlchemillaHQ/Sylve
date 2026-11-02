<script lang="ts">
	import { storage } from '$lib';
	import { getSimpleJailByCTID } from '$lib/api/jail/jail';
	import GuestConsole from '$lib/components/custom/Terminal/GuestConsole.svelte';
	import { jailPowerSignal } from '$lib/stores/api.svelte';
	import type { JailState, SimpleJail } from '$lib/types/jail/jail';
	import { isAPIResponse, updateCache } from '$lib/utils/http';
	import { resource, useInterval, watch } from 'runed';
	import { getContext, onMount, untrack, type Component } from 'svelte';
	import { isDemoMode } from '$lib/demo/runtime';

	interface Data {
		node: string;
		jail: SimpleJail;
		ctId: number;
		hash: string;
	}

	let { data }: { data: Data } = $props();
	const jailState = getContext<{
		current: JailState | null;
		refetch(): Promise<unknown>;
	}>('jailState');
	let runtimeState = $derived(jailState.current?.state ?? 'UNKNOWN');
	let DemoJailConsoleComponent = $state<Component<{
		node: string;
		jailName: string;
	}> | null>(null);
	const initialData = untrack(() => data);
	let consoleIdentity = $derived(`${data.node}\u0000${data.ctId}`);
	let refreshingPowerAction = $state(false);
	let powerRefreshVersion = 0;
	let destroyed = false;

	const storageKey = `node-${initialData.node}-jail-${initialData.ctId}-console`;
	if (typeof localStorage !== 'undefined') {
		for (const suffix of ['state', 'theme']) {
			const legacyValue = localStorage.getItem(`jail-${initialData.ctId}-console-${suffix}`);
			if (localStorage.getItem(`${storageKey}-${suffix}`) === null && legacyValue !== null) {
				localStorage.setItem(`${storageKey}-${suffix}`, legacyValue);
			}
		}
	}

	type JailConsoleSnapshot = { identity: string; jail: SimpleJail };
	const jailResource = resource(
		[() => data.node, () => data.ctId],
		async ([hostname, ctId], _, { signal }): Promise<JailConsoleSnapshot> => {
			const result = await getSimpleJailByCTID(ctId, {
				hostname,
				signal
			});
			if (isAPIResponse(result)) {
				throw new Error(result.message || result.error?.toString() || 'Unable to load jail');
			}
			await updateCache(`simple-jail-${ctId}`, result, hostname);
			return { identity: `${hostname}\u0000${ctId}`, jail: result };
		},
		{
			lazy: true,
			initialValue: {
				identity: `${initialData.node}\u0000${initialData.ctId}`,
				jail: initialData.jail
			}
		}
	);

	const jail = {
		get current(): SimpleJail {
			return jailResource.current.identity === consoleIdentity
				? jailResource.current.jail
				: data.jail;
		},
		refetch: () => jailResource.refetch()
	};

	useInterval(() => 1000, {
		callback: () => {
			if (!storage.visible) return;
			jail.refetch();
		}
	});

	watch(
		() => storage.idle,
		(idle) => {
			if (!idle) {
				jail.refetch();
			}
		}
	);

	watch(
		() => jailPowerSignal.token,
		() => {
			if (jailPowerSignal.ctId !== data.ctId || jailPowerSignal.hostname !== data.node) return;
			const identity = consoleIdentity;
			const version = ++powerRefreshVersion;
			refreshingPowerAction = true;
			void Promise.allSettled([jailState.refetch(), jail.refetch()]).then(() => {
				if (!destroyed && identity === consoleIdentity && version === powerRefreshVersion) {
					refreshingPowerAction = false;
				}
			});
		},
		{ lazy: true }
	);

	onMount(() => {
		if (isDemoMode) {
			void import('$lib/components/custom/Jail/DemoJailConsole.svelte').then((module) => {
				if (!destroyed) DemoJailConsoleComponent = module.default;
			});
		}

		return () => {
			destroyed = true;
		};
	});
</script>

{#if isDemoMode && runtimeState === 'ACTIVE'}
	{#if DemoJailConsoleComponent}
		<DemoJailConsoleComponent node={data.node} jailName={jail.current.name} />
	{:else}
		<div class="bg-background flex h-full w-full items-center justify-center">
			<span class="icon-[mdi--loading] text-primary h-10 w-10 animate-spin"></span>
		</div>
	{/if}
{:else}
	<GuestConsole
		kind="Jail"
		name={jail.current.name}
		url={`/api/jail/${encodeURIComponent(String(data.ctId))}/console`}
		node={data.node}
		hash={data.hash}
		{storageKey}
		status={runtimeState === 'ACTIVE'
			? 'running'
			: runtimeState === 'INACTIVE'
				? 'stopped'
				: 'unknown'}
		pending={refreshingPowerAction || !!jailState.current?.pendingAction}
	/>
{/if}

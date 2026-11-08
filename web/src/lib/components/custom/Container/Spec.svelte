<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts" module>
	export type SpecSection =
		| 'image'
		| 'command'
		| 'environment'
		| 'network'
		| 'ports'
		| 'storage'
		| 'runtime'
		| 'advanced';
</script>

<script lang="ts">
	import {
		getContainerImages,
		getContainerNetworks,
		getMountableDatasets,
		getUsedPorts
	} from '$lib/api/container/resources';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import { Input } from '$lib/components/ui/input/index.js';
	import type { CatalogPlan } from '$lib/types/container/catalog';
	import {
		restartLabels,
		type ContainerPort,
		type ContainerSpec
	} from '$lib/types/container/container';
	import { nextFreePort, portConflict } from '$lib/utils/container/ports';
	import { resource, watch } from 'runed';
	import Catalog from './Catalog.svelte';
	import Mount from './Mount.svelte';
	import MountSuggestions from './MountSuggestions.svelte';

	interface Props {
		spec: ContainerSpec;
		section: SpecSection;
		node: string;
		pool: string;
		onCatalogPick?: (plan: CatalogPlan) => void;
		ocId?: number;
		catalogApp?: string;
	}

	let {
		spec = $bindable(),
		section,
		node,
		pool,
		onCatalogPick,
		ocId,
		catalogApp
	}: Props = $props();

	let catalog = $state({ open: false, resolve: (_image: string) => {} });
	const browseCatalog = () => new Promise<string>((resolve) => (catalog = { open: true, resolve }));

	function pickApp(plan: CatalogPlan) {
		if (!plan.stack) {
			spec.os = plan.containers[0].os;
		}
		catalog.resolve(plan.stack ? spec.image : plan.containers[0].image);
		onCatalogPick?.(plan);
	}

	const images = resource(
		[() => node, () => pool],
		async ([hostname, pool]) => {
			if (section !== 'image' || !pool) return [];
			const images = await getContainerImages(pool, hostname);
			return [...new Set(images.flatMap((image) => image.names))];
		},
		{ initialValue: [] }
	);
	const networks = resource(
		() => node,
		async (hostname) =>
			section === 'network' || section === 'ports' ? await getContainerNetworks(hostname) : [],
		{ initialValue: [] }
	);
	const datasets = resource(
		() => node,
		async (hostname) => (section === 'storage' ? await getMountableDatasets(hostname) : []),
		{ initialValue: [] }
	);
	const usedPorts = resource(
		() => node,
		async (hostname) => (section === 'ports' ? await getUsedPorts(hostname, ocId) : []),
		{ initialValue: [] }
	);
	let portConflicts = $derived(
		spec.ports
			.map((port) => {
				const conflict = portConflict(usedPorts.current, port.protocol, port.hostPort);
				const free = nextFreePort(
					usedPorts.current,
					port.protocol,
					port.hostPort,
					spec.ports.map((other) => other.hostPort)
				);
				return conflict && `${conflict} ${free} is free.`;
			})
			.filter(Boolean)
	);

	const formatPort = (port: ContainerPort) =>
		`${port.hostIp ? `${port.hostIp}:` : ''}${port.hostPort}:${port.containerPort}${port.protocol === 'udp' ? '/udp' : ''}`;

	function parsePort(line: string): ContainerPort {
		const [mapping, protocol = 'tcp'] = line.trim().split('/');
		const [containerPort, hostPort = containerPort, hostIp = ''] = mapping.split(':').reverse();
		return {
			protocol: protocol as ContainerPort['protocol'],
			hostIp,
			hostPort: Number(hostPort),
			containerPort: Number(containerPort)
		};
	}

	const lines = (text: string) => text.split('\n').filter((line) => line.trim());
	const placeholders = {
		env: 'TZ=UTC\nLOG_LEVEL=info',
		ports: '8080:80\n127.0.0.1:5353:53/udp',
		annotations: 'org.freebsd.jail.allow.mlock=true\norg.freebsd.jail.allow.sysvipc=true',
		extraHosts: 'db.local:192.168.1.20'
	};

	let text = $state({
		command: spec.command.join('\n'),
		entrypoint: spec.entrypoint.join('\n'),
		env: spec.env.map((env) => `${env.name}=${env.value}`).join('\n'),
		ports: spec.ports.map(formatPort).join('\n'),
		annotations: Object.entries(spec.annotations)
			.map(([key, value]) => `${key}=${value}`)
			.join('\n'),
		extraHosts: spec.extraHosts.join('\n')
	});
	let open = $state({ image: false, os: false, network: false, restart: false });

	let envText = $state(false);
	function toggleEnvText() {
		if (!envText) {
			text.env = spec.env
				.filter((env) => env.name.trim())
				.map((env) => `${env.name}=${env.value}`)
				.join('\n');
		}
		envText = !envText;
	}
	const secretName = (name: string) => /pass|secret|key|token/i.test(name);

	let networkOptions = $derived([
		...networks.current.map((network) => ({
			value: network.name,
			label: network.name === 'default' ? 'Default' : network.name,
			description: `${network.subnet} · ${network.switchId ? network.bridge : 'NAT'}`
		})),
		...(networks.current.some((network) => network.name === 'default')
			? []
			: [{ value: 'default', label: 'Default', description: 'NAT, created on first use' }]),
		{ value: 'host', label: 'Host', description: 'Share the host network stack' }
	]);
	let selectedNetwork = $derived(networks.current.find((network) => network.name === spec.network));
	let canPublish = $derived(spec.network !== 'host' && !selectedNetwork?.switchId);

	watch(
		() => canPublish,
		(publish) => {
			if (section === 'ports' && !publish) {
				spec.ports = [];
				text.ports = '';
			}
		},
		{ lazy: true }
	);
</script>

<div class="flex flex-col gap-4">
	{#if section === 'image'}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
			<div class="md:col-span-2">
				<CustomComboBox
					bind:open={open.image}
					label="Image"
					placeholder="ghcr.io/daemonless/redis:latest"
					data={images.current.map((name) => ({ value: name, label: name }))}
					bind:value={spec.image}
					allowCustom
					disallowEmpty
					width="w-full"
					classes="space-y-1.5"
					topRightButton={{
						icon: 'icon-[mdi--view-grid-plus-outline]',
						tooltip: 'Browse applications',
						function: browseCatalog
					}}
				/>
			</div>
			<CustomComboBox
				bind:open={open.os}
				label="OS"
				hint="Auto uses the FreeBSD variant of an image when there is one, otherwise Linux"
				data={[
					{ value: 'auto', label: 'Auto' },
					{ value: 'freebsd', label: 'FreeBSD' },
					{ value: 'linux', label: 'Linux' }
				]}
				value={spec.os || 'auto'}
				onValueChange={(value) =>
					(spec.os = value === 'auto' ? '' : (value as ContainerSpec['os']))}
				disallowEmpty
				width="w-full"
				classes="space-y-1.5"
			/>
		</div>
		<CustomCheckbox label="Always pull the image" bind:checked={spec.pullImage} />
	{:else if section === 'command'}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomValueInput
				label="Command"
				placeholder="Image default"
				hint="Replaces the image's command. One argument per line."
				type="textarea"
				textAreaClasses="min-h-24 font-mono"
				bind:value={text.command}
				onChange={() => (spec.command = text.command.split('\n'))}
			/>
			<CustomValueInput
				label="Entrypoint"
				placeholder="Image default"
				hint="Replaces the image's entrypoint. One argument per line."
				type="textarea"
				textAreaClasses="min-h-24 font-mono"
				bind:value={text.entrypoint}
				onChange={() => (spec.entrypoint = text.entrypoint.split('\n'))}
			/>
			<CustomValueInput
				label="Working Directory"
				placeholder="Image default"
				hint="The directory the command runs in. Replaces the image's."
				bind:value={spec.workDir}
			/>
			<CustomValueInput
				label="User"
				placeholder="Image default"
				hint="User name or UID[:GID] to run the command as. Replaces the image's."
				bind:value={spec.user}
			/>
		</div>
	{:else if section === 'environment'}
		{#if envText}
			<CustomValueInput
				label="Environment"
				placeholder={placeholders.env}
				hint="One NAME=value per line"
				type="textarea"
				textAreaClasses="min-h-48 font-mono"
				bind:value={text.env}
				onChange={() =>
					(spec.env = lines(text.env).map((line) => {
						const index = line.indexOf('=');
						return index < 0
							? { name: line.trim(), value: '' }
							: { name: line.slice(0, index).trim(), value: line.slice(index + 1) };
					}))}
			/>
		{:else}
			<div class="-m-1 flex max-h-[55vh] flex-col gap-2 overflow-y-auto p-1">
				{#if spec.env.length > 0}
					<div class="text-muted-foreground flex gap-2 text-xs font-medium">
						<span class="w-2/5">Name</span>
						<span>Value</span>
					</div>
				{/if}
				{#each spec.env as variable, index (index)}
					<div class="flex items-center gap-2">
						<Input
							class="w-2/5 font-mono"
							placeholder="NAME"
							autocomplete="off"
							bind:value={spec.env[index].name}
						/>
						<Input
							class="min-w-0 flex-1 font-mono"
							placeholder="value"
							type={secretName(variable.name) ? 'password' : 'text'}
							showPasswordOnFocus
							autocomplete={secretName(variable.name) ? 'new-password' : 'off'}
							bind:value={spec.env[index].value}
						/>
						<Button
							variant="outline"
							size="icon"
							class="h-9 w-9 shrink-0"
							title="Remove"
							onclick={() => spec.env.splice(index, 1)}
						>
							<span class="icon-[mdi--delete] h-4 w-4"></span>
						</Button>
					</div>
				{:else}
					<div
						class="text-muted-foreground flex flex-col items-center justify-center gap-2 py-8 text-sm"
					>
						<span class="icon-[mdi--variable] h-10 w-10"></span>
						<span>No environment variables</span>
					</div>
				{/each}
			</div>
		{/if}
		<div class="flex items-center gap-2">
			{#if !envText}
				<Button size="sm" class="h-7" onclick={() => spec.env.push({ name: '', value: '' })}>
					<span class="icon-[gg--add] mr-1 h-4 w-4"></span>Add Variable
				</Button>
			{/if}
			<Button size="sm" variant="outline" class="h-7" onclick={toggleEnvText}>
				{#if envText}
					<span class="icon-[mdi--format-list-bulleted] mr-1 h-4 w-4"></span>Edit as List
				{:else}
					<span class="icon-[mdi--text-box-outline] mr-1 h-4 w-4"></span>Paste as Text
				{/if}
			</Button>
		</div>
	{:else if section === 'network'}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
			<CustomComboBox
				bind:open={open.network}
				label="Network"
				data={networkOptions}
				bind:value={spec.network}
				onValueChange={() => (spec.ip = '')}
				disallowEmpty
				width="w-full"
				classes="space-y-1.5"
			/>
			<CustomValueInput
				label="IPv4 Address"
				placeholder={selectedNetwork ? `Automatic from ${selectedNetwork.subnet}` : 'Automatic'}
				bind:value={spec.ip}
				disabled={spec.network === 'host'}
			/>
			<CustomValueInput
				label="Hostname"
				placeholder="Container name"
				bind:value={spec.hostname}
				disabled={spec.network === 'host'}
			/>
		</div>
	{:else if section === 'ports'}
		{#if canPublish}
			<CustomValueInput
				label="Published Ports"
				placeholder={placeholders.ports}
				hint="One host port:container port per line. Prefix a host IP to listen on one address only; add /udp for UDP."
				type="textarea"
				textAreaClasses="min-h-24 font-mono"
				bind:value={text.ports}
				onChange={() => (spec.ports = lines(text.ports).map(parsePort))}
			/>
			{#each portConflicts as conflict, index (index)}
				<p class="-mt-2 flex items-start gap-1.5 text-xs text-amber-600 dark:text-amber-400">
					<span class="icon-[mdi--alert-circle-outline] mt-px h-3.5 w-3.5 shrink-0"></span>
					{conflict}
				</p>
			{/each}
		{:else}
			<p class="text-muted-foreground text-sm">
				{spec.network === 'host'
					? 'On the host network, services listen on host ports directly.'
					: 'On a switch network, the container is reachable at its own address, so ports are not published.'}
			</p>
		{/if}
	{:else if section === 'storage'}
		{#if catalogApp}
			<MountSuggestions
				{catalogApp}
				{node}
				mounts={spec.mounts}
				onAdd={(mount) => spec.mounts.push(mount)}
			/>
		{/if}
		<div class="grid grid-cols-[9rem_1fr_1fr_auto] items-center gap-2">
			{#if spec.mounts.length > 0}
				<span class="text-muted-foreground text-sm font-medium">Type</span>
				<span class="text-muted-foreground text-sm font-medium">Source</span>
				<span class="text-muted-foreground text-sm font-medium">Target</span>
				<span></span>
			{/if}
			{#each spec.mounts as _, index (index)}
				<Mount bind:mount={spec.mounts[index]} datasets={datasets.current}>
					{#snippet actions()}
						<Button
							variant="outline"
							size="icon"
							class="h-9 w-9"
							title="Remove"
							onclick={() => spec.mounts.splice(index, 1)}
						>
							<span class="icon-[mdi--delete] h-4 w-4"></span>
						</Button>
					{/snippet}
				</Mount>
			{:else}
				<div
					class="text-muted-foreground col-span-4 flex flex-col items-center justify-center gap-2 py-8 text-sm"
				>
					<span class="icon-[mdi--folder-off-outline] h-10 w-10"></span>
					<span>No mounts</span>
				</div>
			{/each}
		</div>
		<div>
			<Button
				size="sm"
				class="h-7"
				onclick={() =>
					spec.mounts.push({ type: 'volume', source: '', target: '', readOnly: false })}
			>
				<span class="icon-[gg--add] mr-1 h-4 w-4"></span>Add Mount
			</Button>
		</div>
	{:else if section === 'advanced'}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
			<CustomValueInput
				label="Jail Permissions"
				placeholder={placeholders.annotations}
				hint="One annotation per line; org.freebsd.jail.* ones set jail parameters, such as allow.mlock or allow.sysvipc"
				type="textarea"
				textAreaClasses="min-h-24 font-mono"
				bind:value={text.annotations}
				onChange={() =>
					(spec.annotations = Object.fromEntries(
						lines(text.annotations).map((line) => {
							const index = line.indexOf('=');
							return index < 0
								? [line.trim(), '']
								: [line.slice(0, index).trim(), line.slice(index + 1).trim()];
						})
					))}
			/>
			<CustomValueInput
				label="Extra Hosts"
				placeholder={placeholders.extraHosts}
				hint="One name:address per line, added to the container's /etc/hosts"
				type="textarea"
				textAreaClasses="min-h-24 font-mono"
				bind:value={text.extraHosts}
				onChange={() => (spec.extraHosts = lines(text.extraHosts).map((line) => line.trim()))}
			/>
		</div>
		<CustomValueInput
			label="Shared Memory Size"
			placeholder="Podman default"
			hint="Size of /dev/shm, e.g. 1g; mostly for Linux apps"
			bind:value={spec.shmSize}
		/>
	{:else}
		<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
			<CustomComboBox
				bind:open={open.restart}
				label="Restart Policy"
				data={Object.entries(restartLabels).map(([value, label]) => ({ value, label }))}
				value={spec.restart}
				onValueChange={(value) => (spec.restart = value as ContainerSpec['restart'])}
				disallowEmpty
				width="w-full"
				classes="space-y-1.5"
			/>
			<CustomValueInput
				label="Restart Retries"
				placeholder="Unlimited"
				hint="How many times to restart after a failure; 0 is unlimited"
				type="number"
				bind:value={spec.restartRetries}
				disabled={spec.restart !== 'on-failure'}
			/>
			<CustomValueInput
				label="Stop Timeout (seconds)"
				placeholder="10"
				hint="How long to wait after asking the container to stop before killing it"
				type="number"
				bind:value={spec.stopTimeout}
			/>
		</div>
	{/if}
</div>

{#if catalog.open}
	<Catalog bind:open={catalog.open} {node} onPick={pickApp} />
{/if}

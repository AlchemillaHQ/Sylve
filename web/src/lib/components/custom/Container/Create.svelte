<!--
SPDX-License-Identifier: BSD-2-Clause

Copyright (c) 2025 The FreeBSD Foundation.

This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
under sponsorship from the FreeBSD Foundation.
-->

<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { storage } from '$lib';
	import { getNodes } from '$lib/api/cluster/cluster';
	import { getContainers, newContainer } from '$lib/api/container/container';
	import { getSimpleJails } from '$lib/api/jail/jail';
	import { getLifecycleTask } from '$lib/api/task/lifecycle';
	import { getSimpleVMs } from '$lib/api/vm/vm';
	import { getPools } from '$lib/api/zfs/pool';
	import CustomComboBox from '$lib/components/ui/custom-input/combobox.svelte';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { reload } from '$lib/stores/api.svelte';
	import { newStack } from '$lib/api/container/catalog';
	import type { CatalogPlan } from '$lib/types/container/catalog';
	import { emptyContainerSpec, type ContainerCreateRequest } from '$lib/types/container/container';
	import { isLifecycleTaskActive } from '$lib/types/task/lifecycle';
	import { normalizeSizeInputExact, parseSizeInputToBytes } from '$lib/utils/bytes';
	import { getAPIErrorText, isAPIResponse } from '$lib/utils/http';
	import { getNextGuestId, getNextId } from '$lib/utils/vm/vm';
	import { describeContainerError } from '$lib/utils/container/errors';
	import { createProblem } from '$lib/utils/container/validate';
	import { resource, watch } from 'runed';
	import { toast } from 'svelte-sonner';
	import Spec from './Spec.svelte';

	let { open = $bindable() }: { open: boolean } = $props();

	const tabs = ['Basic', 'Image', 'Network', 'Storage', 'Resources'];
	const specTabs = [
		['Image', ['image', 'command', 'environment']],
		['Network', ['network', 'ports']],
		['Storage', ['storage']]
	] as const;
	const blank = (): ContainerCreateRequest => ({
		...emptyContainerSpec(),
		ocId: 0,
		name: '',
		description: '',
		pool: '',
		cores: 0,
		memory: 0,
		startAtBoot: false,
		startOrder: 0,
		startAfterCreate: true,
		webPort: 0,
		webHttps: false,
		catalogApp: ''
	});

	let node = $state(String(page.params.node || storage.localHostname || storage.hostname || ''));
	let form = $state(blank());
	let memory = $state('');
	let formKey = $state(0);
	let appKey = $state(0);
	let tab = $state('Basic');
	let stack = $state<{ stack: string; containers: ContainerCreateRequest[] } | null>(null);
	let editing = $state<{ index: number; stackForm: ContainerCreateRequest } | null>(null);
	let task = $state<{ id: number; hostname: string; ocId: number; name: string } | null>(null);
	let error = $state('');
	let submitting = $state(false);

	const nodes = resource(
		() => open,
		async () => await getNodes()
	);
	const options = resource(
		() => (open ? node : null),
		async (hostname) => {
			if (!hostname) return null;
			const [pools, vms, jails, containers] = await Promise.all([
				getPools(false, hostname),
				getSimpleVMs(hostname),
				getSimpleJails(hostname),
				getContainers(hostname)
			]);
			return { hostname, pools, vms, jails, containers };
		}
	);
	let clustered = $derived(!!nodes.current && nodes.current.length > 0);
	let openSelect = $state({ node: false, pool: false });

	let nextId = $derived.by(() => {
		if (clustered) return getNextGuestId(nodes.current!);
		if (!options.current) return 0;
		return getNextId(options.current.vms, options.current.jails, options.current.containers);
	});

	watch(
		() => nextId,
		(id) => {
			if (id) form.ocId = id;
		}
	);

	watch(
		() => options.current?.pools,
		(pools) => {
			if (pools && !pools.some((pool) => pool.name === form.pool)) form.pool = pools[0]?.name ?? '';
		}
	);

	const creation = resource(
		() => task,
		async (current) => (current ? await getLifecycleTask(current.id, current.hostname) : null)
	);
	let taskResult = $derived(
		creation.current && !isAPIResponse(creation.current) ? creation.current : null
	);
	let finished = $derived(!!taskResult && !isLifecycleTaskActive(taskResult));
	let creating = $derived(submitting || task !== null);

	$effect(() => {
		if (!task || finished) return;
		const interval = setInterval(() => creation.refetch(), 2000);
		return () => clearInterval(interval);
	});

	watch(
		() => finished,
		(done) => {
			if (!done || !task || !taskResult) return;
			const { hostname, ocId, name } = task;
			const result = taskResult;
			task = null;
			reload.leftPanel = true;
			if (result.status === 'success') {
				toast.success(`${name} created`, { position: 'bottom-center' });
				reset();
				if (open) {
					goto(resolve('/[node]/container/[ocid]/summary', { node: hostname, ocid: String(ocId) }));
				}
				open = false;
			} else {
				error = result.error || `Container ${result.guestId} was not created`;
			}
		}
	);

	function reset() {
		form = { ...blank(), ocId: nextId, pool: form.pool };
		memory = '';
		error = '';
		tab = 'Basic';
		stack = null;
		editing = null;
		formKey += 1;
	}

	function applyApp(plan: CatalogPlan) {
		if (plan.stack) {
			stack = {
				stack: plan.stack,
				containers: plan.containers.map((container) => ({ ...blank(), ...container }))
			};
			error = '';
			return;
		}
		const { name, description, ...spec } = plan.containers[0];
		form = {
			...form,
			...emptyContainerSpec(),
			...spec,
			name: form.name || name,
			description: form.description || description
		};
		appKey += 1;
	}

	function checkedForm(): ContainerCreateRequest | null {
		const bytes = memory.trim() ? parseSizeInputToBytes(memory) : 0;
		const problem =
			bytes === null
				? { message: `Invalid memory size: ${memory}`, tab: 'Resources' }
				: createProblem(form);
		if (problem) {
			toast.error(problem.message, { position: 'bottom-center' });
			tab = problem.tab;
			return null;
		}
		return { ...$state.snapshot(form), memory: bytes ?? 0, cores: Number(form.cores) };
	}

	function editMember(index: number) {
		if (!stack) return;
		const member = $state.snapshot(stack.containers[index]);
		editing = { index, stackForm: $state.snapshot(form) };
		form = { ...member, ocId: form.ocId, pool: form.pool };
		memory = member.memory ? (normalizeSizeInputExact(member.memory) ?? '') : '';
		tab = 'Image';
		formKey += 1;
	}

	function closeMember(save: boolean) {
		if (!stack || !editing) return;
		if (save) {
			const member = checkedForm();
			if (!member) return;
			stack.containers[editing.index] = member;
		}
		form = editing.stackForm;
		memory = '';
		editing = null;
		formKey += 1;
	}

	async function create() {
		const request = checkedForm();
		if (!request) return;
		error = '';
		submitting = true;
		const result = await newContainer(request, node);
		submitting = false;
		if (isAPIResponse(result)) {
			error = getAPIErrorText(result);
			return;
		}
		task = {
			id: result.taskId,
			hostname: node,
			ocId: form.ocId,
			name: `Container ${form.name}`
		};
		reload.leftPanel = true;
	}

	async function createStack() {
		if (!stack) return;
		if (!form.pool || !Number(form.ocId)) {
			toast.error('Choose a pool and the first container ID', { position: 'bottom-center' });
			return;
		}
		error = '';
		submitting = true;
		const plan = $state.snapshot(stack);
		const result = await newStack(
			{
				stack: plan.stack,
				pool: form.pool,
				ocId: Number(form.ocId),
				startAtBoot: form.startAtBoot,
				containers: plan.containers
			},
			node
		);
		submitting = false;
		if (isAPIResponse(result)) {
			error = getAPIErrorText(result);
			return;
		}
		const main = plan.containers.find((container) => container.webPort > 0)?.name;
		task = {
			id: result.taskId,
			hostname: node,
			ocId: result.containers.find((container) => container.name === main)?.ocId ?? result.ocId,
			name: `Stack ${plan.stack}`
		};
		reload.leftPanel = true;
	}
</script>

{#snippet nodeField()}
	<CustomComboBox
		bind:open={openSelect.node}
		label="Node"
		data={(nodes.current ?? []).map((n) => ({ value: n.hostname, label: n.hostname }))}
		bind:value={node}
		disallowEmpty
		width="w-full"
		classes="space-y-1.5"
	/>
{/snippet}

{#snippet poolField(hint: string)}
	<CustomComboBox
		bind:open={openSelect.pool}
		label="Pool"
		{hint}
		data={(options.current?.pools ?? []).map((pool) => ({ value: pool.name, label: pool.name }))}
		bind:value={form.pool}
		disallowEmpty
		width="w-full"
		classes="space-y-1.5"
	/>
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content
		class="fixed left-1/2 top-1/2 flex h-[85vh] w-[80%] -translate-x-1/2 -translate-y-1/2 flex-col gap-0 overflow-auto p-6 lg:h-[72vh] lg:max-w-3xl"
		showCloseButton={true}
		showResetButton={!creating}
		onReset={reset}
		onClose={() => (open = false)}
	>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				<span class="icon-[lucide--container] h-5 w-5"></span>
				<span>{editing ? `Edit ${form.name}` : 'Create Container'}</span>
			</Dialog.Title>
		</Dialog.Header>

		<div class="mt-6 flex-1 overflow-y-auto">
			{#if error}
				{@const problem = describeContainerError(error)}
				<Alert variant="destructive" class="mb-3">
					<AlertTitle>{problem.title}</AlertTitle>
					{#if problem.detail}
						<AlertDescription class="whitespace-pre-wrap break-words">
							{problem.detail}
						</AlertDescription>
					{/if}
				</Alert>
			{/if}

			{#if stack && !editing}
				<fieldset disabled={creating} class="flex flex-col gap-4 p-4">
					<div class="grid grid-cols-1 gap-4 {clustered ? 'md:grid-cols-3' : 'md:grid-cols-2'}">
						{#if clustered}
							{@render nodeField()}
						{/if}
						<CustomValueInput
							label="First Container ID"
							placeholder="100"
							hint="The containers take the free IDs from this one on"
							type="number"
							bind:value={form.ocId}
						/>
						{@render poolField("Holds the containers' images and volumes")}
					</div>
					<div class="space-y-2">
						<p class="text-sm">
							{stack.stack} runs as {stack.containers.length} containers, on a network of their own:
						</p>
						<ul class="divide-y rounded-md border text-sm">
							{#each stack.containers as container, index (container.name)}
								<li class="flex items-center justify-between gap-3 px-3 py-2">
									<div class="min-w-0">
										<p class="font-medium">{container.name}</p>
										<p class="text-muted-foreground truncate font-mono text-xs">
											{container.image}
										</p>
									</div>
									<div class="flex shrink-0 items-center gap-3">
										{#if container.ports.length > 0}
											<span class="text-muted-foreground font-mono text-xs">
												{container.ports
													.map((port) => `${port.hostPort} → ${port.containerPort}`)
													.join(', ')}
											</span>
										{/if}
										<Button
											size="sm"
											variant="outline"
											class="h-7"
											onclick={() => editMember(index)}
										>
											Edit
										</Button>
									</div>
								</li>
							{/each}
						</ul>
						<p class="text-muted-foreground text-xs">
							Edit a container to change its image, ports, environment, storage or resources before
							the stack is created.
						</p>
					</div>
					<CustomCheckbox label="Start On Boot" bind:checked={form.startAtBoot} />
				</fieldset>
			{:else}
				{#key formKey}
					<fieldset disabled={creating}>
						<Tabs.Root bind:value={tab} class="w-full overflow-hidden">
							<Tabs.List class="grid w-full grid-cols-5 p-0">
								{#each tabs as tab (tab)}
									<Tabs.Trigger class="border-b" value={tab}>{tab}</Tabs.Trigger>
								{/each}
							</Tabs.List>

							<Tabs.Content value="Basic">
								<div class="flex flex-col gap-4 p-4">
									{#if editing}
										<CustomValueInput
											label="Name"
											value={form.name}
											placeholder=""
											hint="The stack's other containers reach it by this name"
											disabled
										/>
									{:else}
										<div
											class="grid grid-cols-1 gap-4 {clustered
												? 'md:grid-cols-4'
												: 'md:grid-cols-3'}"
										>
											{#if clustered}
												{@render nodeField()}
											{/if}
											<CustomValueInput label="Name" placeholder="redis" bind:value={form.name} />
											<CustomValueInput
												label="Container ID"
												placeholder="100"
												type="number"
												bind:value={form.ocId}
											/>
											{@render poolField("Holds the container's image and volumes")}
										</div>
									{/if}
									<CustomValueInput
										label="Description"
										placeholder="Optional description for this container"
										type="textarea"
										textAreaClasses="min-h-32"
										bind:value={form.description}
									/>
								</div>
							</Tabs.Content>

							{#each specTabs as [tab, sections] (tab)}
								<Tabs.Content value={tab}>
									<div class="flex flex-col gap-4 p-4">
										{#each sections.filter((section) => !editing || section !== 'network') as section (section)}
											{#key section === 'image' ? -1 : appKey}
												<Spec
													bind:spec={form}
													{section}
													{node}
													pool={form.pool}
													onCatalogPick={editing ? undefined : applyApp}
													catalogApp={form.catalogApp}
												/>
											{/key}
										{/each}
									</div>
								</Tabs.Content>
							{/each}

							<Tabs.Content value="Resources">
								<div class="flex flex-col gap-4 p-4">
									<div class="grid grid-cols-1 gap-4 md:grid-cols-3">
										<CustomValueInput
											label="CPU Cores"
											placeholder="0"
											hint="0 means unlimited"
											type="number"
											bind:value={form.cores}
										/>
										<CustomValueInput
											label="Memory"
											placeholder="Unlimited"
											hint="For example 512M or 2G. Empty means unlimited."
											bind:value={memory}
											onBlur={() => (memory = normalizeSizeInputExact(memory) ?? memory)}
										/>
										<CustomValueInput
											label="Start Order"
											placeholder="0"
											type="number"
											bind:value={form.startOrder}
										/>
									</div>
									{#key appKey}
										<Spec bind:spec={form} section="runtime" {node} pool={form.pool} />
										<Spec bind:spec={form} section="advanced" {node} pool={form.pool} />
									{/key}
									<div class="flex gap-6">
										{#if !editing}
											<CustomCheckbox label="Start On Boot" bind:checked={form.startAtBoot} />
										{/if}
										<CustomCheckbox
											label="Start After Creation"
											bind:checked={form.startAfterCreate}
										/>
									</div>
								</div>
							</Tabs.Content>
						</Tabs.Root>
					</fieldset>
				{/key}
			{/if}
		</div>

		<Dialog.Footer>
			<div class="flex w-full items-center justify-end gap-2">
				{#if creating}
					<p class="text-muted-foreground mr-auto text-sm">
						{stack
							? 'Pulling the images and creating the containers.'
							: 'Pulling the image and creating the container.'} You can close this dialog; the tasks
						continue.
					</p>
				{:else if editing}
					<Button
						size="sm"
						variant="outline"
						class="mr-auto h-8"
						onclick={() => closeMember(false)}
					>
						Cancel
					</Button>
				{:else if stack}
					<Button size="sm" variant="outline" class="mr-auto h-8" onclick={() => (stack = null)}>
						Back
					</Button>
				{/if}
				<Button
					size="sm"
					class="h-8"
					onclick={editing ? () => closeMember(true) : stack ? createStack : create}
					disabled={creating}
				>
					{#if creating}
						<span class="icon-[mdi--loading] h-4 w-4 animate-spin"></span>
					{:else if editing}
						Save Container
					{:else}
						{stack ? 'Create Stack' : 'Create Container'}
					{/if}
				</Button>
			</div>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

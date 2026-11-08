<script lang="ts">
	import EditDialog from '$lib/components/custom/Container/EditDialog.svelte';
	import Properties from '$lib/components/custom/Container/Properties.svelte';
	import Spec, { type SpecSection } from '$lib/components/custom/Container/Spec.svelte';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import {
		containerSpecOf,
		restartLabels,
		type ContainerContext
	} from '$lib/types/container/container';
	import { getContext } from 'svelte';

	let { data }: { data: { node: string } } = $props();

	const container = getContext<ContainerContext>('container');
	let current = $derived(container.current);

	const args = (values: string[]) => values.join(' ') || 'Image default';

	const sections: Record<string, SpecSection> = {
		image: 'image',
		command: 'command',
		entrypoint: 'command',
		workDir: 'command',
		user: 'command',
		environment: 'environment',
		restart: 'runtime',
		stopTimeout: 'runtime',
		annotations: 'advanced',
		extraHosts: 'advanced',
		shmSize: 'advanced'
	};

	let rows = $derived([
		{ id: 'title', property: 'Title', value: current.title || 'Same as the name' },
		{ id: 'name', property: 'Name', value: current.name },
		{
			id: 'image',
			property: 'Image',
			value: `${current.image} (${current.os === 'freebsd' ? 'FreeBSD' : 'Linux'})`
		},
		{ id: 'command', property: 'Command', value: args(current.command) },
		{ id: 'entrypoint', property: 'Entrypoint', value: args(current.entrypoint) },
		{ id: 'workDir', property: 'Working Directory', value: current.workDir || 'Image default' },
		{ id: 'user', property: 'User', value: current.user || 'Image default' },
		{
			id: 'environment',
			property: 'Environment',
			value: current.env.map((env) => env.name).join(', ') || 'None'
		},
		{
			id: 'restart',
			property: 'Restart Policy',
			value:
				restartLabels[current.restart] +
				(current.restart === 'on-failure' && current.restartRetries
					? ` (${current.restartRetries} retries)`
					: '')
		},
		{ id: 'stopTimeout', property: 'Stop Timeout', value: `${current.stopTimeout} seconds` },
		{
			id: 'annotations',
			property: 'Jail Permissions',
			value:
				Object.entries(current.annotations)
					.map(([key, value]) => `${key.replace(/^org\.freebsd\.jail\./, '')}=${value}`)
					.join(', ') || 'None'
		},
		{ id: 'extraHosts', property: 'Extra Hosts', value: current.extraHosts.join(', ') || 'None' },
		{ id: 'shmSize', property: 'Shared Memory Size', value: current.shmSize || 'Podman default' },
		{
			id: 'boot',
			property: 'Start At Boot / Start Order',
			value: `${current.startAtBoot ? 'Yes' : 'No'} / ${current.startOrder}`
		}
	]);

	const titles: Record<string, [string, string]> = {
		title: ['Title', 'icon-[mdi--label-outline]'],
		name: ['Name', 'icon-[mdi--rename]'],
		boot: ['Start At Boot / Start Order', 'icon-[basil--power-button-solid]'],
		image: ['Image', 'icon-[mdi--layers-triple-outline]'],
		command: ['Command', 'icon-[mdi--console-line]'],
		environment: ['Environment', 'icon-[mdi--variable]'],
		runtime: ['Restart and Stop', 'icon-[mdi--restart]'],
		advanced: ['Jail Permissions and Hosts', 'icon-[mdi--shield-key-outline]']
	};

	let dialog = $state({
		open: false,
		id: '',
		spec: containerSpecOf(container.current),
		title: '',
		name: '',
		startAtBoot: false,
		startOrder: 0
	});
	let section = $derived(sections[dialog.id]);
	let [title, icon] = $derived(titles[section ?? dialog.id] ?? ['', '']);

	function edit(id: string) {
		dialog = {
			open: true,
			id,
			spec: containerSpecOf(current),
			title: current.title,
			name: current.name,
			startAtBoot: current.startAtBoot,
			startOrder: current.startOrder
		};
	}

	async function save() {
		if (section) return container.update(dialog.spec);
		if (dialog.id === 'title') return container.setOptions({ title: dialog.title });
		if (dialog.id === 'name') {
			if (dialog.name.trim() === current.name) return true;
			return container.update({ ...dialog.spec, name: dialog.name.trim() });
		}
		return container.setOptions({
			startAtBoot: dialog.startAtBoot,
			startOrder: Number(dialog.startOrder)
		});
	}
</script>

<Properties name="container-options-tt" {rows} disabled={container.busy} onEdit={edit} />

{#if dialog.open}
	<EditDialog
		bind:open={dialog.open}
		{title}
		{icon}
		width={section ? 'sm:max-w-3xl' : ''}
		onSave={save}
	>
		{#if section}
			<Spec bind:spec={dialog.spec} {section} node={data.node} pool={current.pool} />
		{:else if dialog.id === 'title'}
			<CustomValueInput
				label="Title"
				placeholder={current.name}
				hint="Shown for the container; empty shows its name"
				bind:value={dialog.title}
			/>
		{:else if dialog.id === 'name'}
			<CustomValueInput
				label="Name"
				placeholder="redis"
				hint="Its hostname, unless one is set, and the name other containers on its network reach it by. Saving recreates the container."
				bind:value={dialog.name}
			/>
		{:else}
			<div class="flex flex-col gap-4">
				<CustomValueInput
					label="Start Order"
					placeholder="0"
					hint="Containers that start at boot start in ascending order"
					type="number"
					bind:value={dialog.startOrder}
				/>
				<CustomCheckbox label="Start At Boot" bind:checked={dialog.startAtBoot} />
			</div>
		{/if}
	</EditDialog>
{/if}

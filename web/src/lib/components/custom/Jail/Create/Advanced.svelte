<script lang="ts">
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import SimpleSelect from '$lib/components/custom/SimpleSelect.svelte';
	import CustomComboBoxBindable from '$lib/components/ui/custom-input/combobox-bindable.svelte';
	import { ExecPhaseDefs, type ExecPhaseKey, type ExecPhaseState } from '$lib/types/jail/jail';
	import Label from '$lib/components/ui/label/label.svelte';
	import { onMount } from 'svelte';

	interface Props {
		jailType: 'linux' | 'freebsd';
		additionalOptions: string;
		cleanEnvironment: boolean;
		execScripts: Record<ExecPhaseKey, ExecPhaseState>;
		allowedOptions: string[];
		metadata: {
			env: string;
			meta: string;
		};
	}

	let {
		jailType = $bindable(),
		additionalOptions = $bindable(),
		cleanEnvironment = $bindable(),
		execScripts = $bindable(),
		allowedOptions = $bindable(),
		metadata = $bindable()
	}: Props = $props();
	let allowed = [
		{
			value: 'allow.set_hostname',
			label: 'Set Hostname (allow.set_hostname)'
		},
		{
			value: 'allow.raw_sockets',
			label: 'Raw Sockets (allow.raw_sockets)'
		},
		{
			value: 'allow.chflags',
			label: 'Change File Flags (allow.chflags)'
		},
		{
			value: 'allow.mount',
			label: 'Mount Filesystems (allow.mount)'
		},
		{
			value: 'allow.mount.devfs',
			label: 'Mount devfs (allow.mount.devfs)'
		},
		{
			value: 'allow.quotas',
			label: 'FS Quotas (allow.quotas)'
		},
		{
			value: 'allow.read_msgbuf',
			label: 'Read Kernel Message Buffer (allow.read_msgbuf)'
		},
		{
			value: 'allow.socket_af',
			label: 'Socket Address Families (allow.socket_af)'
		},
		{
			value: 'allow.mlock',
			label: 'Memory Locking (allow.mlock)'
		},
		{
			value: 'allow.nfsd',
			label: 'NFS Daemon (allow.nfsd)'
		},
		{
			value: 'allow.reserved_ports',
			label: 'Reserved Ports (allow.reserved_ports)'
		},
		{
			value: 'allow.unprivileged_proc_debug',
			label: 'Unprivileged Process Debugging (allow.unprivileged_proc_debug)'
		},
		{
			value: 'allow.mount.fdescfs',
			label: 'Mount fdescfs (allow.mount.fdescfs)'
		},
		{
			value: 'allow.mount.fusefs',
			label: 'Mount fusefs (allow.mount.fusefs)'
		},
		{
			value: 'allow.mount.nullfs',
			label: 'Mount nullfs (allow.mount.nullfs)'
		},
		{
			value: 'allow.mount.procfs',
			label: 'Mount procfs (allow.mount.procfs)'
		},
		{
			value: 'allow.mount.linprocfs',
			label: 'Mount linprocfs (allow.mount.linprocfs)'
		},
		{
			value: 'allow.mount.linsysfs',
			label: 'Mount linsysfs (allow.mount.linsysfs)'
		},
		{
			value: 'allow.mount.tmpfs',
			label: 'Mount tmpfs (allow.mount.tmpfs)'
		},
		{
			value: 'allow.mount.zfs',
			label: 'Mount ZFS (allow.mount.zfs)'
		},
		{
			value: 'allow.vmm',
			label: 'Virtual Machines (allow.vmm)'
		}
	];

	let comboBoxes = $state({
		allowed: {
			open: false,
			options: allowed
		}
	});

	let checkBoxes = $state({
		additionalOptions: false,
		metadata: {
			env: false,
			meta: false
		}
	});

	function templateSelect() {
		if (jailType === 'freebsd') {
			allowedOptions = [
				'allow.set_hostname',
				'allow.raw_sockets',
				'allow.socket_af',
				'allow.reserved_ports',
				'allow.mount.devfs'
			];

			execScripts['start'].script = '/bin/sh /etc/rc';
			execScripts['stop'].script = '/bin/sh /etc/rc.shutdown';
			execScripts['start'].enabled = true;
			execScripts['stop'].enabled = true;
		} else if (jailType === 'linux') {
			allowedOptions = [
				'allow.set_hostname',
				'allow.raw_sockets',
				'allow.socket_af',
				'allow.mount.devfs',
				'allow.mount.tmpfs',
				'allow.mount.linprocfs',
				'allow.mount.linsysfs'
			];

			execScripts['start'].script = '';
			execScripts['stop'].script = '';
			execScripts['start'].enabled = false;
			execScripts['stop'].enabled = false;
		}
	}

	onMount(() => {
		templateSelect();
	});

	function logClean(x: boolean) {
		console.log('Clean Environment:', x);
	}
</script>

<div class="flex flex-col gap-4 p-4">
	<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
		<SimpleSelect
			label="Type"
			placeholder="Select Jail Type"
			options={[
				{ label: 'FreeBSD', value: 'freebsd' },
				{ label: 'Linux (Experimental)', value: 'linux' }
			]}
			bind:value={jailType}
			onChange={(value) => {
				jailType = value as 'linux' | 'freebsd';
				templateSelect();
			}}
		/>

		<CustomComboBoxBindable
			bind:open={comboBoxes.allowed.open}
			label="Allowed Options"
			placeholder="Select Allowed Options"
			bind:value={allowedOptions}
			data={comboBoxes.allowed.options}
			multiple={true}
			classes="w-full"
			width="w-3/4"
			labelExtraClasses="mb-[3px]"
			showSelected={false}
		/>
	</div>

	<div class="mt-2 flex flex-row gap-2">
		<CustomCheckbox
			label="Clean Environment"
			bind:checked={cleanEnvironment}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Additional Options"
			bind:checked={checkBoxes.additionalOptions}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Metadata (meta)"
			bind:checked={checkBoxes.metadata.meta}
			classes="flex items-center gap-2"
		></CustomCheckbox>

		<CustomCheckbox
			label="Metadata (env)"
			bind:checked={checkBoxes.metadata.env}
			classes="flex items-center gap-2"
		></CustomCheckbox>
	</div>

	{#if checkBoxes.additionalOptions}
		<CustomValueInput
			label="Custom Options"
			placeholder={'### This will be pasted as is into jail config ###'}
			bind:value={additionalOptions}
			classes="flex-1 space-y-1.5"
			textAreaClasses="h-12"
			type="textarea"
			disabled={!checkBoxes.additionalOptions}
		/>
	{/if}

	{#if checkBoxes.metadata.meta}
		<CustomValueInput
			label="Metadata (Meta)"
			placeholder={'### KEY=VALUE pairs, one per line ###\nKEY=VALUE\nKEY2=VALUE2'}
			bind:value={metadata.meta}
			classes="flex-1 space-y-1.5"
			textAreaClasses="h-12"
			type="textarea"
			disabled={!checkBoxes.metadata.meta}
		/>
	{/if}

	{#if checkBoxes.metadata.env}
		<CustomValueInput
			label="Metadata (Environment Variables)"
			placeholder={'### KEY=VALUE pairs, one per line ###\nKEY=VALUE\nKEY2=VALUE2'}
			bind:value={metadata.env}
			classes="flex-1 space-y-1.5"
			textAreaClasses="h-12"
			type="textarea"
			disabled={!checkBoxes.metadata.env}
		/>
	{/if}

	<div class="mt-2 space-y-3">
		<Label>Custom Jail Lifecycle Hooks (exec.* scripts)</Label>

		{#each ExecPhaseDefs as phase}
			<div class="space-y-2 rounded-xl border p-3 md:p-4">
				<div class="flex flex-col gap-1 md:flex-row md:items-center md:justify-between">
					<div>
						<div class="text-sm font-medium">{phase.label}</div>
						<div class="text-muted-foreground text-xs">
							{phase.description}
						</div>
					</div>

					<CustomCheckbox
						label="Enable"
						id={`exec-${phase.key}-enable`}
						bind:checked={execScripts[phase.key].enabled}
						classes="mt-2 flex items-center gap-2 md:mt-0"
					/>
				</div>

				{#if execScripts[phase.key].enabled}
					<CustomValueInput
						label=""
						placeholder={`echo "hello-world"`}
						bind:value={execScripts[phase.key].script}
						classes="flex-1 space-y-1.5"
						textAreaClasses="h-24 text-xs font-mono"
						type="textarea"
					/>

					<div class="text-muted-foreground text-[9px] leading-snug">
						The above script will run during {phase.label}, Ensure they are valid for the host or
						jail
					</div>
				{/if}
			</div>
		{/each}
	</div>
</div>

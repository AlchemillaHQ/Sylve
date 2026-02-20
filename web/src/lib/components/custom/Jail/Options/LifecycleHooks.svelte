<script lang="ts">
	import { modifyLifecycleHooks } from '$lib/api/jail/jail';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { Jail, ExecPhaseKey, ExecPhaseState } from '$lib/types/jail/jail';
	import { ExecPhaseDefs } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		reload: boolean;
	}

	let { open = $bindable(), jail, reload = $bindable() }: Props = $props();

	function emptyScripts(): Record<ExecPhaseKey, ExecPhaseState> {
		return {
			prestart: { enabled: false, script: '' },
			start: { enabled: false, script: '' },
			poststart: { enabled: false, script: '' },
			prestop: { enabled: false, script: '' },
			stop: { enabled: false, script: '' },
			poststop: { enabled: false, script: '' }
		};
	}

	function scriptsFromJail(currentJail: Jail): Record<ExecPhaseKey, ExecPhaseState> {
		const scripts = emptyScripts();
		for (const hook of currentJail.jailHooks || []) {
			if (hook.phase in scripts) {
				const key = hook.phase as ExecPhaseKey;
				scripts[key] = {
					enabled: hook.enabled,
					script: hook.script || ''
				};
			}
		}
		return scripts;
	}

	let execScripts = $derived<Record<ExecPhaseKey, ExecPhaseState>>(scriptsFromJail(jail));

	function reset() {
		execScripts = scriptsFromJail(jail);
	}

	async function save() {
		const response = await modifyLifecycleHooks(jail.ctId, $state.snapshot(execScripts));
		if (response.status === 'error') {
			handleAPIError(response);
			toast.error('Failed to save lifecycle hooks', { position: 'bottom-center' });
			return;
		}

		toast.success('Lifecycle hooks saved', { position: 'bottom-center' });
		reload = !reload;
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="w-[90vw] overflow-hidden p-5 lg:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="flex items-center justify-between">
				<div class="flex items-center gap-2">
					<span class="icon-[iconoir--terminal-outline] h-5 w-5"></span>
					<span>Lifecycle Hooks</span>
				</div>

				<div class="flex items-center gap-0.5">
					<Button size="sm" variant="link" title={'Reset'} class="h-4" onclick={reset}>
						<span class="icon-[radix-icons--reset] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Reset'}</span>
					</Button>
					<Button
						size="sm"
						variant="link"
						class="h-4"
						title={'Close'}
						onclick={() => {
							reset();
							open = false;
						}}
					>
						<span class="icon-[material-symbols--close-rounded] pointer-events-none h-4 w-4"></span>
						<span class="sr-only">{'Close'}</span>
					</Button>
				</div>
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[65vh] space-y-3 overflow-y-auto pr-1">
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
							placeholder={`echo \"hello-world\"`}
							bind:value={execScripts[phase.key].script}
							classes="flex-1 space-y-1.5"
							textAreaClasses="h-24 text-xs font-mono"
							type="textarea"
						/>

						<div class="text-muted-foreground text-[9px] leading-snug">
							The above script will run during {phase.label}, ensure they are valid for the host or
							jail
						</div>
					{/if}
				</div>
			{/each}
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm">Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

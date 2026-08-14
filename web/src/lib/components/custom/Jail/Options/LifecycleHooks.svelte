<script lang="ts">
	import { modifyLifecycleHooks } from '$lib/api/jail/options';
	import SpanWithIcon from '$lib/components/custom/SpanWithIcon.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import CustomCheckbox from '$lib/components/ui/custom-input/checkbox.svelte';
	import CustomValueInput from '$lib/components/ui/custom-input/value.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import Label from '$lib/components/ui/label/label.svelte';
	import type { ExecPhaseKey, ExecPhaseState, Jail } from '$lib/types/jail/jail';
	import { ExecPhaseDefs } from '$lib/types/jail/jail';
	import { handleAPIError } from '$lib/utils/http';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		jail: Jail;
		node: string;
		onSaved: () => void | Promise<void>;
	}

	let { open = $bindable(), jail, node, onSaved }: Props = $props();

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
				scripts[key] = { enabled: hook.enabled, script: hook.script || '' };
			}
		}
		return scripts;
	}

	// svelte-ignore state_referenced_locally
	let execScripts = $state<Record<ExecPhaseKey, ExecPhaseState>>(scriptsFromJail(jail));
	let saving = $state(false);

	function reset() {
		execScripts = scriptsFromJail(jail);
	}

	async function save() {
		if (saving) return;
		const missingScript = ExecPhaseDefs.find(
			(phase) => execScripts[phase.key].enabled && !execScripts[phase.key].script.trim()
		);
		if (missingScript) {
			toast.error(`${missingScript.label} requires a script when enabled`, {
				position: 'bottom-center'
			});
			return;
		}

		saving = true;
		try {
			const response = await modifyLifecycleHooks(jail.ctId, $state.snapshot(execScripts), {
				hostname: node
			});
			if (response.status === 'error') {
				handleAPIError(response);
				toast.error('Failed to save lifecycle hooks', { position: 'bottom-center' });
				return;
			}

			await onSaved();
			toast.success('Lifecycle hooks saved', { position: 'bottom-center' });
			open = false;
		} finally {
			saving = false;
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content
		class="w-[90vw] overflow-hidden p-6 lg:max-w-2xl"
		showCloseButton={!saving}
		showResetButton={!saving}
		onReset={reset}
		onClose={() => {
			if (saving) return;
			reset();
			open = false;
		}}
		onEscapeKeydown={(event) => {
			if (saving) event.preventDefault();
		}}
		aria-busy={saving}
	>
		<Dialog.Header>
			<Dialog.Title>
				<SpanWithIcon
					icon="icon-[iconoir--terminal-outline]"
					size="h-5 w-5"
					gap="gap-2"
					title="Lifecycle Hooks"
				/>
			</Dialog.Title>
		</Dialog.Header>

		<div class="max-h-[65vh] space-y-3 overflow-y-auto pr-1">
			<Label>Custom Jail Lifecycle Hooks (exec.* scripts)</Label>

			{#each ExecPhaseDefs as phase (phase.key)}
				<div class="space-y-2 rounded-xl border p-3 md:p-4">
					<div class="flex flex-col gap-1 md:flex-row md:items-center md:justify-between">
						<div>
							<div class="text-sm font-medium">{phase.label}</div>
							<div class="text-muted-foreground text-xs">{phase.description}</div>
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
							placeholder="echo &quot;hello-world&quot;"
							bind:value={execScripts[phase.key].script}
							classes="flex-1 space-y-1.5"
							textAreaClasses="h-24 text-xs font-mono"
							type="textarea"
						/>

						<div class="text-muted-foreground text-[9px] leading-snug">
							This script runs during {phase.label}; ensure it is valid for the corresponding host
							or jail environment.
						</div>
					{/if}
				</div>
			{/each}
		</div>

		<Dialog.Footer class="flex justify-end">
			<Button onclick={save} size="sm" disabled={saving} aria-busy={saving}>
				{#if saving}
					<span class="icon-[mdi--loading] mr-1 h-4 w-4 animate-spin"></span>
					Saving...
				{:else}
					Save
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

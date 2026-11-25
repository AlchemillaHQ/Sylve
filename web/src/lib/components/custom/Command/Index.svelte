<script lang="ts">
	import { storage } from '$lib';
	import * as Command from '$lib/components/ui/command/index.js';

	import { mode, toggleMode } from 'mode-watcher';
	import { logOut } from '$lib/api/auth';
	import { useSafeGoto } from '$lib/hooks/navigation.svelte';
	import { setInterface, ui } from '$lib/stores/interface.svelte';
	import { allowed, nodePath } from '$lib/stores/permissions.svelte';
</script>

<Command.Dialog bind:open={storage.openCommands} class="rounded-lg border shadow-md md:min-w-112.5">
	<Command.Input placeholder="Type a command..." />

	<Command.List>
		<Command.Empty>No results found.</Command.Empty>
		<Command.Group heading="Suggestions">
			{#if !ui.zen || allowed('Sys.Console', nodePath(storage.hostname || ''))}
				<Command.Item
					value="host-terminal"
					onSelect={() => {
						storage.openCommands = false;
						useSafeGoto(`/${storage.hostname}/terminal`);
					}}
				>
					<span class="size-5 {ui.zen ? 'icon-[lucide--terminal]' : 'icon-[mdi--console]'}"></span>
					<span>Host Terminal</span>
				</Command.Item>
			{/if}

			<Command.Item
				value="switch-interface"
				onSelect={() => {
					storage.openCommands = false;
					setInterface(ui.zen ? 'classic' : 'zen');
				}}
			>
				<span class="icon-[lucide--arrow-left-right] size-5"></span>
				<span>Switch interface</span>
				<span class="ms-auto text-xs text-muted-foreground">
					{#if ui.zen}Zen{:else}Classic{/if}
				</span>
			</Command.Item>

			<Command.Item
				value="toggle-theme"
				onSelect={() => {
					toggleMode();
					storage.openCommands = false;
				}}
				class="rounded-xl!"
			>
				<span class="icon-[lucide--sun-moon] size-5"></span>
				<span>Toggle theme</span>
				<span class="ms-auto text-xs text-muted-foreground">
					{mode.current === 'dark' ? 'Dark' : 'Light'}
				</span>
			</Command.Item>

			<Command.Item
				value="logout"
				onSelect={() => {
					storage.openCommands = false;
					logOut();
				}}
			>
				<span class="icon-[lucide--log-out] size-5"></span>
				<span>Log out</span>
			</Command.Item>
		</Command.Group>
	</Command.List>
</Command.Dialog>

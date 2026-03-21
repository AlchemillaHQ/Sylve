<script lang="ts">
	import { storage } from '$lib';
	import * as Command from '$lib/components/ui/command/index.js';

	import { mode, toggleMode } from 'mode-watcher';
	import { logOut } from '$lib/api/auth';
	import { goto } from '$app/navigation';
</script>

<Command.Dialog
	bind:open={storage.openCommands}
	class="rounded-lg border shadow-md md:min-w-[450px]"
>
	<Command.Input placeholder="Type a command..." />

	<Command.List>
		<Command.Empty>No results found.</Command.Empty>
		<Command.Group heading="Suggestions">
			<Command.Item
				value="host-terminal"
				onSelect={() => {
					storage.openCommands = false;
					goto(`/${storage.hostname}/terminal`);
				}}
			>
				<span class="icon-[mdi--console] size-5"></span>
				<span>Host Terminal</span>
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

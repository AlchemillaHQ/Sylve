<script>
	import { getJWTClaims, logOut } from '$lib/api/auth';
	import ReplicationActivity from '$lib/components/custom/DataCenter/Replication/Activity.svelte';
	import { Button } from '$lib/components/ui/button/index.js';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { mode, toggleMode } from 'mode-watcher';
	import CreateJail from './Jail/Create/CreateJail.svelte';
	import CreateVM from './VM/Create/CreateVM.svelte';
	import { storage, languageArr } from '$lib';
	import { loadLocale } from 'wuchale/load-utils';

	let options = {
		createVM: {
			open: false,
			minimize: false
		},
		createJail: {
			open: false,
			minimize: false
		}
	};

	let properties = $state(options);
	let jwt = $state(getJWTClaims());
	let mobileMenuOpen = $state(false);

	let virtualizationEnabled = $derived(
		Boolean(storage.enabledServices?.includes('virtualization'))
	);
	let jailEnabled = $derived(Boolean(storage.enabledServices?.includes('jails')));

	function openCreateVM() {
		properties.createVM.open = true;
		properties.createVM.minimize = false;
	}

	function openCreateJail() {
		properties.createJail.open = true;
		properties.createJail.minimize = false;
	}
</script>

<header
	class="bg-background sticky top-0 z-40 flex h-14 items-center gap-3 border-x border-b px-2 md:h-[4vh]"
>
	<div class="flex items-center lg:hidden">
		<Sheet.Root bind:open={mobileMenuOpen}>
			<Sheet.Trigger class="flex items-center">
				<Button variant="outline" size="icon" class="h-8 w-8 shrink-0">
					<span class="icon-[material-symbols--menu-rounded] h-6 w-6"></span>
					<span class="sr-only">Toggle navigation menu</span>
				</Button>
			</Sheet.Trigger>
			<Sheet.Content side="left" class="w-[86%] p-0 sm:max-w-sm">
				<!-- mobile view -->
				<div class="flex h-full flex-col">
					<div class="border-b px-5 py-4">
						<div class="flex items-center gap-3">
							{#if mode.current === 'dark'}
								<img src="/logo/white.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
							{:else}
								<img src="/logo/black.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
							{/if}
							<div class="flex flex-col">
								<!-- @wc-ignore -->
								<p class="font-normal tracking-[.35em]">SYLVE</p>
								<p class="text-muted-foreground text-xs">Quick actions</p>
							</div>
						</div>
					</div>

					<nav class="flex flex-1 flex-col gap-2 px-4 py-4 text-sm">
						<p class="text-muted-foreground text-xs font-semibold uppercase tracking-wide">
							Create
						</p>

						{#if virtualizationEnabled}
							<Button
								variant="outline"
								class="relative h-10 w-full justify-start gap-2"
								onclick={() => {
									openCreateVM();
									mobileMenuOpen = false;
								}}
							>
								<span class="icon-[material-symbols--monitor-outline-rounded] h-4 w-4"></span>
								<span>Create VM</span>

								{#if properties.createVM.minimize}
									<span
										class="absolute right-2 h-3 w-3 rounded-full border border-white bg-yellow-400"
										title="VM creation form minimized"
									></span>
								{/if}
							</Button>
						{/if}

						{#if jailEnabled}
							<Button
								variant="outline"
								class="relative h-10 w-full justify-start gap-2"
								onclick={() => {
									openCreateJail();
									mobileMenuOpen = false;
								}}
							>
								<span class="icon-[hugeicons--prison] h-4 w-4"></span>
								<span>Create Jail</span>

								{#if properties.createJail.minimize}
									<span
										class="absolute right-2 h-3 w-3 rounded-full border border-white bg-yellow-400"
										title="Jail creation form minimized"
									></span>
								{/if}
							</Button>
						{/if}

						{#if !virtualizationEnabled && !jailEnabled}
							<p class="text-muted-foreground rounded-md border border-dashed px-3 py-2 text-sm">
								No create actions are available.
							</p>
						{/if}
					</nav>
				</div>
			</Sheet.Content>
		</Sheet.Root>
	</div>

	<div class="flex items-center space-x-2 lg:hidden">
		{#if mode.current === 'dark'}
			<img src="/logo/white.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
		{:else}
			<img src="/logo/black.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
		{/if}
		<!-- @wc-ignore -->
		<p class="text-sm font-normal tracking-[.35em]">SYLVE</p>
	</div>

	<nav
		class="hidden flex-col gap-2 text-lg font-medium md:items-center md:gap-2 md:text-sm lg:flex lg:flex-row lg:gap-4"
	>
		<div class="flex items-center space-x-2">
			{#if mode.current === 'dark'}
				<img src="/logo/white.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
			{:else}
				<img src="/logo/black.svg" alt="Sylve Logo" class="h-6 w-auto max-w-25" />
			{/if}
			<!-- @wc-ignore -->
			<p class="font-normal tracking-[.45em]">SYLVE</p>
		</div>
	</nav>
	<div class="flex w-full items-center justify-end gap-2 md:ml-auto lg:gap-4">
		<div class="hidden items-center gap-4 lg:flex">
			{#if storage.showReplication}
				<ReplicationActivity />
			{/if}

			{#if virtualizationEnabled}
				<Button class="relative h-6" size="sm" onclick={openCreateVM}>
					<div class="flex items-center gap-2">
						<span class="icon-[material-symbols--monitor-outline-rounded] h-4 w-4"></span>
						<span>Create VM</span>
					</div>

					{#if properties.createVM.minimize}
						<span
							class="absolute -right-1 -top-0.5 h-3 w-3 rounded-full border border-white bg-yellow-400"
							title="VM creation form minimized"
						></span>
					{/if}
				</Button>
			{/if}

			{#if jailEnabled}
				<Button class="relative h-6" size="sm" onclick={openCreateJail}>
					<div class="flex items-center gap-2">
						<span class="icon-[hugeicons--prison] h-4 w-4"></span>
						<span>Create Jail</span>
					</div>

					{#if properties.createJail.minimize}
						<span
							class="absolute -right-1 -top-0.5 h-3 w-3 rounded-full border border-white bg-yellow-400"
							title="Jail creation form minimized"
						></span>
					{/if}
				</Button>
			{/if}
		</div>

		{#if properties.createVM.open || properties.createVM.minimize}
			<CreateVM bind:open={properties.createVM.open} bind:minimize={properties.createVM.minimize} />
		{/if}

		{#if properties.createJail.open || properties.createJail.minimize}
			<CreateJail
				bind:open={properties.createJail.open}
				bind:minimize={properties.createJail.minimize}
			/>
		{/if}

		<div class="flex items-center">
			<DropdownMenu.Root>
				<DropdownMenu.Trigger class="flex items-center">
					<Button variant="outline" size="sm" class="relative h-6">
						<div class="flex items-center gap-2">
							<span class="icon-[mdi--user] h-4 w-4"></span>

							<span class="hidden sm:inline">{jwt?.custom_claims.username}</span>
							<span class="icon-[famicons--chevron-down] h-4 w-4"></span>
						</div>
						<span class="sr-only">Toggle user menu</span></Button
					>
				</DropdownMenu.Trigger>
				<DropdownMenu.Content class="w-56">
					<DropdownMenu.Group>
						<DropdownMenu.Item class="cursor-pointer" onclick={toggleMode}>
							<span class="icon-[mdi--palette] mr-2 h-4 w-4"></span>
							<span>Color Theme</span>
						</DropdownMenu.Item>

						<DropdownMenu.Sub>
							<DropdownMenu.Root>
								<DropdownMenu.Trigger
									class=" hover:bg-accent h-6.5 flex w-full cursor-pointer items-center justify-start px-2.5 py-4 text-left"
								>
									<span class="icon-[meteor-icons--language] mr-4 h-4 w-4"></span>
									Language
								</DropdownMenu.Trigger>
								<DropdownMenu.Content class="w-48">
									<DropdownMenu.Group>
										{#each languageArr as { value, label } (value)}
											<DropdownMenu.CheckboxItem
												class="cursor-pointer"
												checked={storage.language === value}
												onclick={() => {
													storage.language = value;
													loadLocale(value);
												}}
											>
												{label}
											</DropdownMenu.CheckboxItem>
										{/each}
									</DropdownMenu.Group>
								</DropdownMenu.Content>
							</DropdownMenu.Root></DropdownMenu.Sub
						>
					</DropdownMenu.Group>

					<DropdownMenu.Separator />
					<DropdownMenu.Item class="cursor-pointer" onclick={() => logOut()}>
						<span class="icon-[ic--twotone-logout] mr-2 h-4 w-4"></span>
						<span>Log Out</span>
					</DropdownMenu.Item>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		</div>
	</div>
</header>

<script lang="ts">
	import { storage } from '$lib';
	import { getDefaultTitle, terminalStore } from '$lib/stores/terminal.svelte';
	import { sha256 } from '$lib/utils/string';
	import {
		Xterm,
		XtermAddon,
		type FitAddon,
		type ITerminalInitOnlyOptions,
		type ITerminalOptions,
		type Terminal
	} from '@battlefieldduck/xterm-svelte';
	import adze from 'adze';
	import { untrack } from 'svelte';
	import { fade, scale } from 'svelte/transition';

	let terminal = $state<Terminal>();
	let ws = $state<WebSocket>();
	let fitAddonGlobal = $state<FitAddon>();
	let options: ITerminalOptions & ITerminalInitOnlyOptions = {
		cursorBlink: true
	};

	let tabsCount = $derived.by(() => {
		return terminalStore.tabs?.length || 0;
	});

	let currentTab = $derived.by(() => {
		return terminalStore.tabs.find((tab) => tab.id === terminalStore.activeTabId);
	});

	async function killSession(sessionId: string): Promise<boolean> {
		return new Promise((resolve) => {
			if (!ws || ws.readyState !== WebSocket.OPEN) {
				resolve(false);
				return;
			}

			const onMessage = (event: MessageEvent) => {
				if (event.data) {
					if (typeof event.data === 'string') {
						if (event.data.includes(`Session killed: ${sessionId}`)) {
							ws?.removeEventListener('message', onMessage);
							resolve(true);
						}
					}
				}
			};

			ws.addEventListener('message', onMessage);
			ws.send(new TextEncoder().encode('\x02' + JSON.stringify({ kill: sessionId })));

			setTimeout(() => {
				ws?.removeEventListener('message', onMessage);
				resolve(false);
			}, 2000);
		});
	}

	async function onLoad() {
		try {
			if (!currentTab) return;

			ws?.close();
			terminal?.clear();
			terminal?.reset();

			const fitAddon = new (await XtermAddon.FitAddon()).FitAddon();
			terminal?.loadAddon(fitAddon);
			fitAddon.fit();

			const hash = await sha256(storage.token || '', 1);

			ws = new WebSocket(`/api/info/terminal?id=${currentTab?.id}&hash=${hash}`);
			ws.binaryType = 'arraybuffer';
			ws.onopen = () => {
				if (!currentTab) return;
				adze.info(`Terminal WebSocket connected for tab ${currentTab?.id}`);
				if (terminal) {
					const dimensions = fitAddon.proposeDimensions();
					(ws as WebSocket).send(
						new TextEncoder().encode(
							'\x01' + JSON.stringify({ rows: dimensions?.rows, cols: dimensions?.cols })
						)
					);

					fitAddonGlobal = fitAddon;
				}
			};

			ws.onmessage = (event) => {
				if (event.data instanceof ArrayBuffer) {
					if (terminal) {
						terminal.write(new Uint8Array(event.data));
					}
				}
			};

			ws.onclose = () => {
				if (!currentTab) return;
				adze.info(`Terminal WebSocket disconnected for tab ${currentTab?.id}`);
			};
		} catch (e) {
			adze.error('Failed to connect to terminal WebSocket', { error: e });
		}
	}

	function onData(data: string) {
		ws?.send(new TextEncoder().encode('\x00' + data));
	}

	async function visiblityAction(t: string, e?: MouseEvent | string) {
		if (t === 'window-minimize') {
			terminalStore.isMinimized = true;
			return;
		}

		if (t === 'window-close') {
			const tabsToKill = [...terminalStore.tabs];
			for (const tab of tabsToKill) {
				await killSession(tab.id);
			}

			terminalStore.tabs = [];
			terminalStore.isOpen = false;
			ws?.close();
		}

		if (t === 'tab-close') {
			const event = e as MouseEvent;
			if (event) {
				const target = event.target as HTMLElement;
				const parent = target.closest('button');
				if (parent) {
					const tabId = parent.getAttribute('data-id');
					if (tabId) {
						await killSession(tabId);
						terminalStore.tabs = terminalStore.tabs.filter((tab) => tab.id !== tabId);
						if (terminalStore.tabs.length > 0) {
							terminalStore.activeTabId = terminalStore.tabs[0].id;
						}
					}
				}
			}
		}

		if (t === 'tab-select') {
			const tabId = e as string;
			terminalStore.activeTabId = tabId;
		}
	}

	function addTab() {
		const terminalCount = terminalStore.tabs.length;
		let tabId = `sylve-${terminalCount + 1}`;

		const newTab = {
			id: tabId,
			title: getDefaultTitle()
		};

		terminalStore.tabs = [...terminalStore.tabs, newTab];
		terminalStore.activeTabId = newTab.id;
	}

	let innerWidth = $state(0);

	$effect(() => {
		if (innerWidth) {
			untrack(() => {
				fitAddonGlobal?.fit();
				const dimensions = fitAddonGlobal?.proposeDimensions();
				ws?.send(
					new TextEncoder().encode(
						'\x01' + JSON.stringify({ rows: dimensions?.rows, cols: dimensions?.cols })
					)
				);
			});
		}
	});
</script>

<svelte:window bind:innerWidth />

{#if terminalStore.isOpen && !terminalStore.isMinimized}
	<div class="z-9998 fixed inset-0 bg-black/30 backdrop-blur-sm transition-all duration-150"></div>
	<div
		class="z-9999 fixed inset-0 flex items-center justify-center transition-all duration-150"
		in:scale={{ start: 0.9, duration: 150 }}
		out:scale={{ start: 0.9, duration: 150 }}
	>
		<div
			class="border-muted bg-muted-foreground/10 relative flex w-[60%] flex-col rounded-lg border-4"
		>
			<div class="bg-primary-foreground flex items-center justify-between p-2">
				<!-- Add Tab Button -->
				<div class="flex items-center gap-2">
					<span>{terminalStore.title}</span>
				</div>
				<!-- Minimize / Close -->
				<div class="flex space-x-3">
					<button
						class="flex justify-center rounded-full transition-colors duration-300 ease-in-out hover:bg-yellow-600 hover:text-white"
						onclick={() => visiblityAction('window-minimize')}
						title="Minimize"
					>
						<span class="icon-[mdi--window-minimize] h-5 w-5"></span>
					</button>
					<button
						class="flex justify-center rounded-full transition-colors duration-300 ease-in-out hover:bg-red-500 hover:text-white"
						onclick={() => visiblityAction('window-close')}
						title="Close"
					>
						<span class="icon-[mdi--close] h-5 w-5"></span>
					</button>
				</div>
			</div>

			<!-- Available Tabs -->
			<div class="dark:bg-muted/30 flex overflow-x-auto bg-white">
				{#each terminalStore.tabs as tab}
					<div
						class="border-muted-foreground/40 flex cursor-pointer items-center px-3.5 py-2 {tab.id ===
						terminalStore.activeTabId
							? 'bg-muted-foreground/40 dark:bg-muted-foreground/25 '
							: 'border-muted-foreground/25 hover:bg-muted-foreground/25 border-x border-t'}"
						onclick={() => visiblityAction('tab-select', tab.id)}
						onkeydown={(e) =>
							(e.key === 'Enter' || e.key === ' ') && visiblityAction('tab-select', tab.id)}
						role="button"
						tabindex="0"
					>
						<span class="mr-2 whitespace-nowrap text-sm">{tab.title}</span>
						{#if tabsCount > 1}
							<button
								class="flex justify-center rounded-full transition-colors duration-300 ease-in-out hover:bg-red-500 hover:text-white"
								data-id={tab.id}
								onclick={(e) => {
									e.stopPropagation();
									visiblityAction('tab-close', e);
								}}
							>
								<span class="icon-[mdi--close] h-5 w-5"></span>
							</button>
						{/if}
					</div>
				{/each}
				<div
					class="hover:border-muted-foreground/30 hover:bg-muted-foreground/30 flex items-center justify-center border px-1"
				>
					<button
						class="dark:hover-bg-muted flex h-6 w-6 items-center justify-center rounded"
						onclick={() => addTab()}
						title="Add new tab"
					>
						<span class="icon-[ic--sharp-plus] h-5 w-5"></span>
					</button>
				</div>
			</div>

			<!-- Terminal Body -->
			<div id="terminal-container" class="relative min-h-0 w-full grow overflow-hidden bg-black">
				{#each terminalStore.tabs as tab}
					{#if tab.id === terminalStore.activeTabId}
						<div in:fade={{ duration: 150 }}>
							<Xterm bind:terminal {options} {onLoad} {onData} />
						</div>
					{/if}
				{/each}
			</div>
		</div>
	</div>
{/if}

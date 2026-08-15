/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { DemoVMProfile } from '$lib/demo/vm-profiles';
import { formatBytesBinary } from '$lib/utils/bytes';
import type { V86 as V86Instance, V86Image, V86Options } from 'v86';
import v86WasmUrl from 'v86/build/v86.wasm?url';
import seabiosUrl from './assets/seabios.bin?url';
import vgabiosUrl from './assets/vgabios.bin?url';

export type DemoVMRuntimeStatus = 'idle' | 'loading' | 'running' | 'paused' | 'error';

export type DemoVMRuntimeSnapshot = {
	status: DemoVMRuntimeStatus;
	text: string;
	progress: number;
	progressDeterminate: boolean;
};

const maxSerialTranscriptLength = 256 * 1024;
const maxCachedRuntimes = 2;
const runtimes = new Map<string, { runtime: DemoVMRuntime; lastUsed: number }>();

function evictRuntime(runtimeKey: string): void {
	const entry = runtimes.get(runtimeKey);
	if (!entry) return;
	runtimes.delete(runtimeKey);
	void entry.runtime.destroy();
}

function pruneRuntimes(activeKey: string): void {
	while (runtimes.size > maxCachedRuntimes) {
		const oldest = [...runtimes.entries()]
			.filter(([key]) => key !== activeKey)
			.sort(([, left], [, right]) => left.lastUsed - right.lastUsed)[0];
		if (!oldest) return;
		evictRuntime(oldest[0]);
	}
}

function toV86Image(image: DemoVMProfile['emulator']['image']): V86Image {
	if (!image.async) return { url: image.url };
	return {
		url: image.url,
		async: true,
		size: image.size,
		use_parts: image.useParts,
		fixed_chunk_size: image.fixedChunkSize
	};
}

function delay(milliseconds: number) {
	return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

function safeHostname(value: string): string {
	return value.replace(/[^a-zA-Z0-9.-]/g, '-') || 'demo-vm';
}

export class DemoVMRuntime {
	private emulator: V86Instance | null = null;
	private screenContainer: HTMLElement | null = null;
	private parkingContainer: HTMLElement | null = null;
	private startPromise: Promise<void> | null = null;
	private generation = 0;
	private serialTranscript = '';
	private serialDecoder = new TextDecoder();
	private snapshot: DemoVMRuntimeSnapshot = {
		status: 'idle',
		text: 'Connecting to demo VM',
		progress: 0,
		progressDeterminate: false
	};
	private listeners = new Set<(snapshot: DemoVMRuntimeSnapshot) => void>();
	private serialListeners = new Set<(output: string) => void>();
	private screenResizeListeners = new Set<() => void>();

	constructor(
		private readonly profile: DemoVMProfile,
		readonly vmName: string
	) {
		this.snapshot.text = `Connecting to ${vmName}`;
	}

	subscribe(listener: (snapshot: DemoVMRuntimeSnapshot) => void): () => void {
		this.listeners.add(listener);
		listener(this.snapshot);
		return () => this.listeners.delete(listener);
	}

	subscribeSerial(listener: (output: string) => void): () => void {
		this.serialListeners.add(listener);
		return () => this.serialListeners.delete(listener);
	}

	subscribeScreenResize(listener: () => void): () => void {
		this.screenResizeListeners.add(listener);
		return () => this.screenResizeListeners.delete(listener);
	}

	getSerialTranscript(): string {
		return this.serialTranscript;
	}

	attachScreen(host: HTMLElement): () => void {
		const screen = this.ensureScreenContainer();
		host.replaceChildren(screen);
		for (const listener of this.screenResizeListeners) listener();

		return () => {
			if (screen.parentElement !== host) return;
			this.ensureParkingContainer().append(screen);
		};
	}

	fitScreen(viewport: HTMLElement): void {
		const emulator = this.emulator;
		const screen = this.screenContainer;
		if (!emulator || !screen) return;

		emulator.screen_set_scale(1, 1);
		const display = Array.from(screen.children).find(
			(child): child is HTMLElement =>
				child instanceof HTMLElement && window.getComputedStyle(child).display !== 'none'
		);
		if (!display) return;

		const natural = display.getBoundingClientRect();
		const available = viewport.getBoundingClientRect();
		if (!natural.width || !natural.height || !available.width || !available.height) return;

		const availableScale = Math.min(
			available.width / natural.width,
			available.height / natural.height
		);
		const scale =
			this.profile.id === 'freebsd-i386' && availableScale >= 1
				? Math.floor(availableScale)
				: availableScale;
		if (Number.isFinite(scale) && scale > 0) emulator.screen_set_scale(scale, scale);
	}

	setPointerEnabled(enabled: boolean): void {
		this.emulator?.mouse_set_enabled(enabled);
	}

	lockPointer(): void {
		this.emulator?.lock_mouse();
	}

	sendSerial(data: string): void {
		if (!this.emulator?.is_running()) return;
		this.emulator.serial_send_bytes(0, new TextEncoder().encode(data));
	}

	start(): Promise<void> {
		if (this.emulator) {
			if (!this.emulator.is_running()) {
				void this.emulator.run();
				this.updateStatus(
					'running',
					`${this.vmName} is running locally in this browser`,
					100,
					true
				);
			}
			return this.startPromise ?? Promise.resolve();
		}

		if (this.startPromise) return this.startPromise;
		this.startPromise = this.createEmulator().finally(() => {
			this.startPromise = null;
		});
		return this.startPromise;
	}

	async retry(): Promise<void> {
		const active = this.emulator;
		this.emulator = null;
		this.generation += 1;
		if (active) await active.destroy();
		this.resetScreenContainer();
		this.serialTranscript = '';
		this.serialDecoder = new TextDecoder();
		this.updateStatus('idle', `Connecting to ${this.vmName}`, 0, false);
		await this.start();
	}

	async destroy(): Promise<void> {
		this.generation += 1;
		const active = this.emulator;
		this.emulator = null;
		this.startPromise = null;
		this.serialTranscript = '';
		this.serialDecoder = new TextDecoder();
		if (active) await active.destroy();
		this.screenContainer?.remove();
		this.parkingContainer?.remove();
		this.screenContainer = null;
		this.parkingContainer = null;
		this.updateStatus('idle', `Connecting to ${this.vmName}`, 0, false);
	}

	async handlePowerAction(action: string): Promise<void> {
		const emulator = this.emulator;
		if (action === 'start') {
			await this.start();
			return;
		}
		if (!emulator) return;

		if (action === 'reboot') {
			emulator.restart();
			this.updateStatus('running', `${this.vmName} is restarting`, 100, true);
			return;
		}

		if (action === 'stop' || action === 'shutdown') {
			await emulator.stop();
			this.updateStatus('paused', `${this.vmName} is stopped`, 100, true);
		}
	}

	private ensureScreenContainer(): HTMLElement {
		if (this.screenContainer) return this.screenContainer;

		const screen = document.createElement('div');
		screen.className = 'sylve-demo-v86-screen';
		screen.style.cssText =
			'position:relative;display:block;overflow:hidden;background:#000;line-height:0;';
		this.screenContainer = screen;
		this.resetScreenContainer();
		this.ensureParkingContainer().append(screen);
		return screen;
	}

	private resetScreenContainer(): void {
		const screen = this.screenContainer;
		if (!screen) return;

		const textScreen = document.createElement('div');
		textScreen.style.cssText =
			'white-space:pre;font:15px/16px ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,"Liberation Mono","Courier New",monospace;min-width:720px;min-height:400px;background:#000;color:#ddd;transform-origin:top left;';
		const canvas = document.createElement('canvas');
		canvas.style.display = 'none';
		screen.replaceChildren(textScreen, canvas);
	}

	private ensureParkingContainer(): HTMLElement {
		if (this.parkingContainer) return this.parkingContainer;
		const parking = document.createElement('div');
		parking.setAttribute('aria-hidden', 'true');
		parking.style.cssText =
			'position:fixed;left:-10000px;top:0;width:720px;height:400px;overflow:hidden;opacity:0;pointer-events:none;';
		document.body.append(parking);
		this.parkingContainer = parking;
		return parking;
	}

	private emulatorOptions(): V86Options {
		const options: V86Options = {
			wasm_path: v86WasmUrl,
			memory_size: this.profile.memoryBytes,
			vga_memory_size: 8 * 1024 ** 2,
			screen: {
				container: this.ensureScreenContainer(),
				scaling: 1,
				use_graphical_text: this.profile.id !== 'freebsd-i386'
			},
			bios: { url: seabiosUrl },
			vga_bios: { url: vgabiosUrl },
			disable_speaker: true,
			disable_mouse: false,
			autostart: true
		};

		if (this.profile.emulator.kind === 'hda') {
			options.hda = toV86Image(this.profile.emulator.image);
			if (this.profile.emulator.initialStateUrl) {
				options.initial_state = { url: this.profile.emulator.initialStateUrl };
				options.preserve_mac_from_state_image = true;
			}
		} else {
			options.bzimage = toV86Image(this.profile.emulator.image);
			options.filesystem = {};
			options.cmdline = this.profile.emulator.cmdline;
		}

		return options;
	}

	private async createEmulator(): Promise<void> {
		const generation = ++this.generation;
		this.updateStatus('loading', `Loading ${this.profile.label}`, 0, false);

		try {
			const { V86 } = await import('v86');
			if (generation !== this.generation) return;

			const emulator = new V86(this.emulatorOptions());
			this.emulator = emulator;
			emulator.add_listener('serial0-output-byte', this.handleSerialByte);
			emulator.add_listener('screen-set-size', this.handleScreenResize);

			emulator.add_listener('download-progress', (event) => {
				if (generation !== this.generation || emulator !== this.emulator) return;
				const fileName =
					typeof event?.file_name === 'string'
						? event.file_name.split('/').at(-1)?.split('?')[0] || this.profile.label
						: this.profile.label;
				const loaded = Number.isFinite(event?.loaded) ? event.loaded : null;
				const total = Number.isFinite(event?.total) && event.total > 0 ? event.total : null;

				if (loaded !== null && total !== null) {
					this.updateStatus(
						'loading',
						`Loading ${fileName} · ${formatBytesBinary(loaded)} of ${formatBytesBinary(total)}`,
						Math.min(100, Math.round((loaded / total) * 100)),
						true
					);
				} else if (loaded !== null) {
					this.updateStatus(
						'loading',
						`Loading ${fileName} · ${formatBytesBinary(loaded)}`,
						0,
						false
					);
				} else {
					this.updateStatus('loading', `Loading ${fileName}`, 0, false);
				}
			});

			emulator.add_listener('download-error', () => {
				if (generation !== this.generation || emulator !== this.emulator) return;
				this.updateStatus('error', 'The browser VM image could not be loaded.', 0, false);
			});

			emulator.add_listener('emulator-ready', () => {
				if (generation !== this.generation || emulator !== this.emulator) return;
				this.updateStatus('loading', `Starting ${this.vmName}`, 100, true);
			});

			emulator.add_listener('emulator-started', () => {
				if (generation !== this.generation || emulator !== this.emulator) return;
				this.updateStatus(
					'running',
					`${this.vmName} is running locally in this browser`,
					100,
					true
				);
				if (this.profile.id === 'tinycore-x86') emulator.mouse_set_enabled(false);
				this.handleScreenResize();
				void this.prepareSerial(emulator, generation);
			});
		} catch (error) {
			if (generation !== this.generation) return;
			this.updateStatus(
				'error',
				error instanceof Error ? error.message : 'Unable to start the browser VM.',
				0,
				false
			);
		}
	}

	private enableSerialPort(emulator: V86Instance): void {
		emulator.serial_set_carrier_detect(0, true);
		emulator.serial_set_data_set_ready(0, true);
		emulator.serial_set_clear_to_send(0, true);
	}

	private async prepareSerial(emulator: V86Instance, generation: number): Promise<void> {
		this.enableSerialPort(emulator);
		if (this.profile.id === 'tinycore-x86') {
			await this.prepareTinyCore(emulator, generation);
			return;
		}

		if (this.profile.id !== 'freebsd-i386') return;
		try {
			await emulator.wait_until_vga_screen_contains(/#\s*$/, { timeout_msec: 15_000 });
			if (generation !== this.generation || emulator !== this.emulator) return;
			await emulator.keyboard_send_text(
				`hostname ${safeHostname(this.vmName)}; sed -i '' 's#^ttyu0.*#ttyu0 "/usr/libexec/getty al.115200" xterm on secure#' /etc/ttys; kill -HUP 1; clear\n`
			);
		} catch {
			if (generation === this.generation && emulator === this.emulator) {
				this.updateStatus(
					'running',
					`${this.vmName} is running; serial startup is still in progress`,
					100,
					true
				);
			}
		}
	}

	private async prepareTinyCore(emulator: V86Instance, generation: number): Promise<void> {
		this.updateStatus('running', `Booting ${this.vmName}`, 100, true);
		try {
			await emulator.wait_until_vga_screen_contains(/Press ENTER to boot/i, {
				timeout_msec: 15_000
			});
			if (generation !== this.generation || emulator !== this.emulator) return;

			emulator.keyboard_send_scancodes([0x0f, 0x8f]);
			await delay(120);
			await emulator.keyboard_send_text(
				' text base norestore xvesa=800x600x24 console=tty0 console=ttyS0,115200n8'
			);
			await delay(250);
			emulator.keyboard_send_scancodes([0x1c, 0x9c]);
			emulator.keyboard_send_keys([13]);

			await emulator.wait_until_vga_screen_contains(/tc@box:~\$/, { timeout_msec: 35_000 });
			if (generation !== this.generation || emulator !== this.emulator) return;

			const hostname = safeHostname(this.vmName);
			await emulator.keyboard_send_text(
				`sudo hostname ${hostname}; sudo sh -c "printf '#!/bin/sh\\nPS1=\\"root@${hostname}:~ # \\"\\nexport PS1\\nexec /bin/sh -i\\n' > /tmp/serial-shell; chmod +x /tmp/serial-shell; echo 'ttyS0::respawn:/tmp/serial-shell' >> /etc/inittab"; (sleep 4; sudo kill -HUP 1) & startx\n`
			);
			this.updateStatus('running', `${this.vmName} is running locally in this browser`, 100, true);
		} catch {
			if (generation === this.generation && emulator === this.emulator) {
				this.updateStatus(
					'running',
					`${this.vmName} is running; guest startup is still in progress`,
					100,
					true
				);
			}
		}
	}

	private updateStatus(
		status: DemoVMRuntimeStatus,
		text: string,
		progress: number,
		progressDeterminate: boolean
	): void {
		this.snapshot = { status, text, progress, progressDeterminate };
		for (const listener of this.listeners) listener(this.snapshot);
	}

	private handleSerialByte = (byte: number) => {
		const output = this.serialDecoder.decode(Uint8Array.of(byte), { stream: true });
		if (!output) return;
		this.serialTranscript += output;
		if (this.serialTranscript.length > maxSerialTranscriptLength) {
			this.serialTranscript = this.serialTranscript.slice(-maxSerialTranscriptLength);
		}
		for (const listener of this.serialListeners) listener(output);
	};

	private handleScreenResize = () => {
		for (const listener of this.screenResizeListeners) listener();
	};
}

export function getDemoVMRuntime(
	key: string,
	profile: DemoVMProfile,
	vmName: string
): DemoVMRuntime {
	const runtimeKey = `${profile.id}:${key}`;
	let entry = runtimes.get(runtimeKey);
	if (entry && entry.runtime.vmName !== vmName) {
		evictRuntime(runtimeKey);
		entry = undefined;
	}
	if (!entry) {
		entry = { runtime: new DemoVMRuntime(profile, vmName), lastUsed: Date.now() };
		runtimes.set(runtimeKey, entry);
	} else {
		entry.lastUsed = Date.now();
	}
	pruneRuntimes(runtimeKey);
	return entry.runtime;
}

export function evictDemoVMRuntime(key: string): void {
	for (const runtimeKey of runtimes.keys()) {
		if (runtimeKey.endsWith(`:${key}`)) evictRuntime(runtimeKey);
	}
}

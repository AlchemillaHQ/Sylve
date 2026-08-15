/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { Terminal } from '@battlefieldduck/xterm-svelte';
import type { V86 as V86Instance, V86Image, V86Options } from 'v86';
import v86WasmUrl from 'v86/build/v86.wasm?url';
import { formatBytesBinary } from '$lib/utils/bytes';
import { getDemoVMProfile } from './vm-profiles';
import seabiosUrl from './assets/seabios.bin?url';
import vgabiosUrl from './assets/vgabios.bin?url';

export type DemoHostTerminalStatus = 'idle' | 'loading' | 'running' | 'error';

export type DemoHostTerminalSnapshot = {
	status: DemoHostTerminalStatus;
	text: string;
	progress: number;
};

const freeBSDProfile = getDemoVMProfile('freebsd-i386');
const demoHostNetwork = {
	relayUrl: 'wisps://wisp.sylve.io/',
	routerIp: '192.168.86.1',
	vmIp: '192.168.86.100',
	netmask: '255.255.255.0'
} as const;

const maxSerialTranscriptLength = 512 * 1024;

function toV86Image(image: NonNullable<typeof freeBSDProfile>['emulator']['image']): V86Image {
	if (!image.async) return { url: image.url };
	return {
		url: image.url,
		async: true,
		size: image.size,
		use_parts: image.useParts,
		fixed_chunk_size: image.fixedChunkSize
	};
}

function normalizeHostname(hostname: string): string {
	return (
		hostname
			.trim()
			.toLowerCase()
			.replace(/[^a-z0-9.-]/g, '') || 'leto'
	);
}

function formatGuestUTCDate(date: Date): string {
	const part = (value: number) => value.toString().padStart(2, '0');
	return `${date.getUTCFullYear()}${part(date.getUTCMonth() + 1)}${part(date.getUTCDate())}${part(date.getUTCHours())}${part(date.getUTCMinutes())}.${part(date.getUTCSeconds())}`;
}

async function sendKeyboardTextWithDelay(
	emulator: V86Instance,
	text: string,
	delayMilliseconds: number
): Promise<void> {
	for (const character of text) {
		emulator.keyboard_send_text(character);
		await new Promise<void>((resolve) => setTimeout(resolve, delayMilliseconds));
	}
}

class DemoHostTerminalRuntime {
	private emulator: V86Instance | null = null;
	private screenContainer: HTMLElement | null = null;
	private terminal: Terminal | null = null;
	private startPromise: Promise<void> | null = null;
	private requestedHostname = 'leto';
	private appliedHostname = '';
	private hostnameRequest = 0;
	private serialReady = false;
	private serialTranscript = '';
	private serialDecoder = new TextDecoder();
	private serialReplayInProgress = false;
	private pendingSerialBytes: number[] = [];
	private serialFlushQueued = false;
	private requestedColumns = 80;
	private requestedRows = 25;
	private resizeTimeout = 0;
	private serialStartupTimeout = 0;
	private snapshot: DemoHostTerminalSnapshot = {
		status: 'idle',
		text: 'FreeBSD demo host',
		progress: 0
	};
	private listeners = new Set<(snapshot: DemoHostTerminalSnapshot) => void>();

	registerScreenContainer(screenContainer: HTMLElement) {
		this.screenContainer = screenContainer;
	}

	subscribe(listener: (snapshot: DemoHostTerminalSnapshot) => void): () => void {
		this.listeners.add(listener);
		listener(this.snapshot);
		return () => this.listeners.delete(listener);
	}

	setHostname(hostname: string) {
		const normalized = normalizeHostname(hostname);
		if (normalized === this.requestedHostname && normalized === this.appliedHostname) return;

		this.requestedHostname = normalized;
		const request = ++this.hostnameRequest;
		if (this.serialReady) void this.applyHostnameToSerial(request);
	}

	start(): Promise<void> {
		if (this.snapshot.status === 'running' || this.snapshot.status === 'loading') {
			return this.startPromise ?? Promise.resolve();
		}
		if (!this.screenContainer) return Promise.resolve();

		this.startPromise = this.createEmulator().finally(() => {
			this.startPromise = null;
		});
		return this.startPromise;
	}

	attach(terminal: Terminal): () => void {
		this.terminal = terminal;
		this.replaySerialTranscript();
		this.resize(terminal.cols, terminal.rows);

		if (!this.serialTranscript) {
			terminal.write(
				'\u001b[90mThe FreeBSD serial console is starting in the background…\u001b[0m\r\n'
			);
		}

		if (this.snapshot.status === 'error') void this.retry();
		else if (this.snapshot.status === 'idle') void this.start();

		return () => {
			if (this.terminal === terminal) this.terminal = null;
		};
	}

	send(data: string) {
		const emulator = this.emulator;
		if (this.serialReplayInProgress || !emulator?.is_running()) return;
		emulator.serial_send_bytes(0, new TextEncoder().encode(data));
	}

	resize(columns: number, rows: number) {
		this.requestedColumns = Math.max(20, Math.min(300, Math.round(columns)));
		this.requestedRows = Math.max(5, Math.min(120, Math.round(rows)));
		if (!this.serialReady) return;

		window.clearTimeout(this.resizeTimeout);
		this.resizeTimeout = window.setTimeout(() => void this.applySerialSize(), 150);
	}

	refresh() {
		this.replaySerialTranscript();
	}

	async retry() {
		if (this.snapshot.status !== 'error') {
			await this.start();
			return;
		}

		const failed = this.emulator;
		this.emulator = null;
		this.resetSerialState();
		if (failed) await failed.destroy();
		await this.start();
	}

	async destroy(screenContainer?: HTMLElement) {
		if (screenContainer && screenContainer !== this.screenContainer) return;

		this.hostnameRequest += 1;
		window.clearTimeout(this.resizeTimeout);
		window.clearTimeout(this.serialStartupTimeout);
		this.resizeTimeout = 0;
		this.serialStartupTimeout = 0;
		this.terminal = null;
		this.screenContainer = null;
		const active = this.emulator;
		this.emulator = null;
		this.resetSerialState();
		if (active) await active.destroy();
		this.updateStatus('idle', 'FreeBSD demo host', 0);
	}

	private updateStatus(status: DemoHostTerminalStatus, text: string, progress: number) {
		this.snapshot = { status, text, progress };
		for (const listener of this.listeners) listener(this.snapshot);
	}

	private resetSerialState() {
		this.serialReady = false;
		this.serialTranscript = '';
		this.serialDecoder = new TextDecoder();
		this.serialReplayInProgress = false;
		this.pendingSerialBytes = [];
		this.serialFlushQueued = false;
		this.appliedHostname = '';
	}

	private replaySerialTranscript() {
		const terminal = this.terminal;
		if (!terminal || !this.serialTranscript) return;

		this.serialReplayInProgress = true;
		terminal.write(`\u001bc${this.serialTranscript}`, () => {
			this.serialReplayInProgress = false;
		});
	}

	private handleSerialByte = (byte: number) => {
		this.pendingSerialBytes.push(byte);
		if (this.serialFlushQueued) return;
		this.serialFlushQueued = true;
		queueMicrotask(this.flushSerialOutput);
	};

	private flushSerialOutput = () => {
		this.serialFlushQueued = false;
		if (!this.pendingSerialBytes.length) return;

		const bytes = Uint8Array.from(this.pendingSerialBytes);
		this.pendingSerialBytes = [];
		const output = this.serialDecoder.decode(bytes, { stream: true });
		if (!output) return;

		this.serialTranscript += output;
		if (this.serialTranscript.length > maxSerialTranscriptLength) {
			this.serialTranscript = this.serialTranscript.slice(-maxSerialTranscriptLength);
		}
		this.terminal?.write(output);

		if (!this.serialReady && /root@[a-z0-9.-]+:[^\r\n]*#\s*$/.test(this.serialTranscript)) {
			this.serialReady = true;
			this.appliedHostname = this.requestedHostname;
			window.clearTimeout(this.serialStartupTimeout);
			this.serialStartupTimeout = 0;
			this.updateStatus('running', 'FreeBSD serial console', 100);
			this.resize(this.requestedColumns, this.requestedRows);
		}
	};

	private async applySerialSize() {
		const emulator = this.emulator;
		if (!this.serialReady || !emulator?.is_running()) return;

		await sendKeyboardTextWithDelay(
			emulator,
			`stty -f /dev/ttyu0 columns ${this.requestedColumns} rows ${this.requestedRows} >& /dev/null; clear\n`,
			2
		);
	}

	private async applyHostnameToSerial(request: number) {
		const emulator = this.emulator;
		if (!this.serialReady || !emulator?.is_running()) return;
		if (request !== this.hostnameRequest) return;

		const hostname = this.requestedHostname;
		emulator.serial_send_bytes(
			0,
			new TextEncoder().encode(
				`\u0003hostname ${hostname}; set prompt = 'root@${hostname}:%~ # '; clear\r`
			)
		);
		this.appliedHostname = hostname;
	}

	private async startSerialConsole(request: number) {
		const emulator = this.emulator;
		if (!emulator?.is_running()) return;

		await emulator.wait_until_vga_screen_contains(/#\s*$/, { timeout_msec: 15_000 });
		if (request !== this.hostnameRequest || emulator !== this.emulator || !emulator.is_running()) {
			return;
		}

		emulator.serial_set_carrier_detect(0, true);
		emulator.serial_set_data_set_ready(0, true);
		emulator.serial_set_clear_to_send(0, true);

		const hostname = this.requestedHostname;
		const guestUTCDate = formatGuestUTCDate(new Date());
		await sendKeyboardTextWithDelay(
			emulator,
			`ifconfig ed0 inet ${demoHostNetwork.vmIp} netmask ${demoHostNetwork.netmask} >& /dev/null; route add default ${demoHostNetwork.routerIp} >& /dev/null; printf 'nameserver ${demoHostNetwork.routerIp}\\n' > /etc/resolv.conf; date -u ${guestUTCDate} >& /dev/null; hostname ${hostname}; sed -i '' 's#^ttyu0.*#ttyu0 "/usr/libexec/getty al.115200" xterm on secure#' /etc/ttys; kill -HUP 1; clear\n`,
			8
		);

		this.serialStartupTimeout = window.setTimeout(() => {
			if (emulator !== this.emulator || this.serialReady) return;
			this.updateStatus('error', 'The FreeBSD serial console did not start.', 100);
			this.terminal?.write(
				'\r\n\u001b[31mThe FreeBSD serial console did not start. Use Reconnect to try again.\u001b[0m\r\n'
			);
		}, 12_000);
	}

	private async createEmulator() {
		if (!freeBSDProfile || freeBSDProfile.emulator.kind !== 'hda') {
			this.updateStatus('error', 'The FreeBSD demo profile is unavailable.', 0);
			return;
		}
		if (!this.screenContainer) return;

		this.resetSerialState();
		this.updateStatus('loading', 'Loading FreeBSD demo host', 0);

		try {
			const { V86 } = await import('v86');
			if (!this.screenContainer) return;

			const options: V86Options = {
				wasm_path: v86WasmUrl,
				memory_size: freeBSDProfile.memoryBytes,
				vga_memory_size: 8 * 1024 ** 2,
				screen_container: this.screenContainer,
				bios: { url: seabiosUrl },
				vga_bios: { url: vgabiosUrl },
				hda: toV86Image(freeBSDProfile.emulator.image),
				initial_state: freeBSDProfile.emulator.initialStateUrl
					? { url: freeBSDProfile.emulator.initialStateUrl }
					: undefined,
				net_device: {
					type: 'ne2k',
					relay_url: demoHostNetwork.relayUrl,
					router_ip: demoHostNetwork.routerIp,
					vm_ip: demoHostNetwork.vmIp,
					masquerade: true,
					dns_method: 'doh'
				},
				preserve_mac_from_state_image: true,
				disable_speaker: true,
				disable_mouse: true,
				autostart: true
			};

			const emulator = new V86(options);
			this.emulator = emulator;
			emulator.keyboard_set_enabled(false);
			emulator.add_listener('serial0-output-byte', this.handleSerialByte);

			emulator.add_listener('download-progress', (event) => {
				if (emulator !== this.emulator) return;
				const label = event.file_name.split('/').at(-1) || freeBSDProfile.label;
				const progress =
					event.lengthComputable && event.total > 0
						? Math.min(100, Math.round((event.loaded / event.total) * 100))
						: 0;
				const text =
					event.lengthComputable && event.total > 0
						? `Loading ${label} · ${formatBytesBinary(event.loaded)} of ${formatBytesBinary(event.total)}`
						: `Loading ${label}`;
				this.updateStatus('loading', text, progress);
			});

			emulator.add_listener('download-error', () => {
				if (emulator !== this.emulator) return;
				this.updateStatus('error', 'The FreeBSD demo host could not be loaded.', 0);
				this.terminal?.write(
					'\r\n\u001b[31mThe FreeBSD demo host could not be loaded. Use Reconnect to try again.\u001b[0m\r\n'
				);
			});

			emulator.add_listener('emulator-ready', () => {
				if (emulator !== this.emulator) return;
				this.updateStatus('loading', 'Starting FreeBSD', 100);
			});

			emulator.add_listener('emulator-started', () => {
				if (emulator !== this.emulator) return;
				this.updateStatus('loading', 'Starting FreeBSD serial console', 100);
				const hostnameRequest = ++this.hostnameRequest;
				setTimeout(() => void this.startSerialConsole(hostnameRequest), 50);
			});
		} catch (error) {
			const message = error instanceof Error ? error.message : 'Unable to start the demo host.';
			this.updateStatus('error', message, 0);
			this.terminal?.write(`\r\n\u001b[31m${message}\u001b[0m\r\n`);
		}
	}
}

export const demoHostTerminal = new DemoHostTerminalRuntime();

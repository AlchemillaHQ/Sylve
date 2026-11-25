/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2026 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected' | 'error';

interface Options {
	getUrl: () => Promise<string>;
	onStatus: (status: ConnectionStatus) => void;
	onOutput: (data: string | Uint8Array) => void;
}

export function createHostConnection({ getUrl, onStatus, onOutput }: Options) {
	let socket: WebSocket | null = null;
	let attempt = 0;
	let disposed = false;
	let timeout: ReturnType<typeof setTimeout> | undefined;
	const encoder = new TextEncoder();

	function closeSocket() {
		clearTimeout(timeout);
		timeout = undefined;
		const current = socket;
		socket = null;
		if (!current) return;
		current.onopen = null;
		current.onmessage = null;
		current.onerror = null;
		current.onclose = null;
		current.close();
	}

	function fail() {
		attempt += 1;
		closeSocket();
		onStatus('error');
	}

	async function connect() {
		if (disposed) return;
		const activeAttempt = ++attempt;
		closeSocket();
		onStatus('connecting');
		const isCurrent = () => !disposed && activeAttempt === attempt;
		timeout = setTimeout(() => {
			if (isCurrent()) fail();
		}, 15_000);

		try {
			const url = await getUrl();
			if (!isCurrent()) return;
			const current = new WebSocket(url);
			current.binaryType = 'arraybuffer';
			socket = current;

			current.onopen = () => {
				if (!isCurrent()) return;
				clearTimeout(timeout);
				timeout = undefined;
				onStatus('connected');
			};
			current.onmessage = (event) => {
				if (!isCurrent()) return;
				if (event.data instanceof ArrayBuffer) onOutput(new Uint8Array(event.data));
				else if (typeof event.data === 'string') onOutput(event.data);
			};
			current.onerror = () => {
				if (isCurrent()) fail();
			};
			current.onclose = (event) => {
				if (!isCurrent()) return;
				attempt += 1;
				closeSocket();
				onStatus(event.code === 1000 ? 'disconnected' : 'error');
			};
		} catch {
			if (isCurrent()) fail();
		}
	}

	function send(control: string, data: string) {
		if (!socket || socket.readyState !== WebSocket.OPEN) return;
		try {
			socket.send(encoder.encode(control + data));
		} catch {
			fail();
		}
	}

	return {
		connect,
		send(data: string) {
			send('\x00', data);
		},
		resize(cols: number, rows: number) {
			if (cols > 0 && rows > 0) send('\x01', JSON.stringify({ rows, cols }));
		},
		disconnect() {
			if (disposed) return;
			send('\x02', JSON.stringify({ kill: '' }));
			attempt += 1;
			closeSocket();
			onStatus('disconnected');
		},
		dispose() {
			disposed = true;
			attempt += 1;
			closeSocket();
		}
	};
}

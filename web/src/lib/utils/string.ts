/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import { getIcon, loadIcon } from '@iconify/svelte';
import isCidr from 'is-cidr';
import { isIP, isIPv4, isIPv6 } from 'is-ip';
import { decode as magnetDecode, encode as magnetEncode } from 'magnet-uri';
import { customRandom, nanoid } from 'nanoid';
import isEmail from 'validator/lib/isEmail';
import isMACAddress from 'validator/lib/isMACAddress';
import isURL from 'validator/lib/isURL';
import { Mnemonic } from './vendor/mnemonic';

export function capitalizeFirstLetter(str: string, firstOnly: boolean = false): string {
	if (firstOnly) {
		return str.charAt(0).toLocaleUpperCase() + str.slice(1);
	}

	return str
		.split(' ')
		.map((word) => word.charAt(0).toLocaleUpperCase() + word.slice(1))
		.join(' ');
}

export function parseJwt(token: string) {
	let base64Url = token.split('.')[1];
	let base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
	let jsonPayload = decodeURIComponent(
		window
			.atob(base64)
			.split('')
			.map(function (c) {
				return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
			})
			.join('')
	);

	return JSON.parse(jsonPayload);
}

export function shortenString(str: string, maxLength: number): string {
	if (str.length <= maxLength) return str;
	return str.slice(0, maxLength) + '...';
}

export function generatePassword(): string {
	return new Mnemonic().toWords().slice(0, 6).join('-');
}

export async function iconToSVG(icon: string): Promise<string> {
	await loadIcon(icon);
	const i = getIcon(icon);
	if (i) {
		const { body, width, height, left, top } = i;
		const viewBox = `${left} ${top} ${width} ${height}`;
		const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="${viewBox}">${body}</svg>`;
		return svg;
	}

	return ''; // Ensure the function always returns a string
}

function seedRandom(seed: string): () => number {
	let h = 2166136261 >>> 0;
	for (let i = 0; i < seed.length; i++) {
		h ^= seed.charCodeAt(i);
		h = Math.imul(h, 16777619);
	}
	let state = h;

	return function () {
		state = (state * 1664525 + 1013904223) >>> 0;
		return (state >>> 0) / 4294967296;
	};
}

export function generateNanoId(seed?: string): string {
	if (seed) {
		const rng = seedRandom(seed);
		const customNanoId = customRandom('abcdefghijklmnopqrstuvwxyz', 10, (size) => {
			return new Uint8Array(size).map(() => 256 * rng());
		});

		return customNanoId();
	}

	return nanoid(10);
}

export function isValidSwitchName(name: string): boolean {
	const regex = /^[a-zA-Z0-9-_]+$/;
	return regex.test(name);
}

export function isValidIPv4(ip: string, cidr: boolean = false): boolean {
	try {
		if (cidr && isCidr.v4(ip)) {
			return true;
		}

		if (!cidr && isIPv4(ip)) {
			return true;
		}

		return false;
	} catch (e) {
		console.log(e);
		return false;
	}
}

export function isValidIPv6(ip: string, cidr: boolean = false): boolean {
	try {
		if (cidr && isCidr.v6(ip)) {
			return true;
		}

		if (!cidr && isIPv6(ip)) {
			return true;
		}

		return false;
	} catch (e) {
		console.log(e);
		return false;
	}
}

export function isDownloadURL(url: string): boolean {
	const urlOpts = {
		protocols: ['http', 'https'],
		require_protocol: true,
		require_valid_protocol: true,
		require_host: true,
		allow_underscores: false,
		allow_fragments: false,
		allow_query_components: true
	};

	if (!isURL(url, urlOpts)) {
		return false;
	}

	try {
		const { pathname } = new URL(url);
		return /\/[^\/]+\.[^\/]+$/.test(pathname);
	} catch {
		return false;
	}
}

export function isValidAbsPath(loc: string): boolean {
	return loc.length > 0 && loc.startsWith('/');
}

export function isValidVMName(name: string): boolean {
	const regex = /^[a-zA-Z0-9-_]+$/;
	return regex.test(name);
}

export function isValidMACAddress(mac: string): boolean {
	return isMACAddress(mac, { no_colons: false });
}

export async function sha256(str: string, rounds: number = 1): Promise<string> {
	const encoder = new TextEncoder();
	let data = encoder.encode(str);

	for (let i = 0; i < rounds; i++) {
		const hashBuffer = await crypto.subtle.digest('SHA-256', data);
		data = new Uint8Array(hashBuffer);
	}

	const hashArray = Array.from(data);
	return hashArray.map((b) => b.toString(16).padStart(2, '0')).join('');
}

export function isValidUsername(username: string): boolean {
	const invalidUsernames = ['root', 'admin', 'superuser'];
	if (invalidUsernames.includes(username.toLowerCase())) {
		return false;
	}

	const regex = /^[a-z_]([a-z0-9_-]{0,31}|[a-z0-9_-]{0,30}\$)$/;
	return regex.test(username);
}

export function isValidEmail(email: string): boolean {
	return isEmail(email, {
		require_tld: true,
		allow_utf8_local_part: true,
		allow_display_name: false
	});
}

export function addTrackersToMagnet(uri: string): string {
	try {
		const parsed = magnetDecode(uri);
		if (!parsed.tr || parsed.tr.length === 0) {
			const trackers = [
				'udp://tracker.opentrackr.org:1337/announce',
				'udp://tracker.coppersurfer.tk:6969/announce',
				'udp://tracker.internetwarriors.net:1337/announce',
				'udp://tracker.openbittorrent.com:80/announce',
				'udp://tracker.publicbt.com:80/announce'
			];

			parsed.tr = trackers;
			parsed.announce = trackers;
		}

		return magnetEncode(parsed);
	} catch (e) {
		console.error('Invalid magnet URI:', e);
	}

	return uri;
}

export function isValidFileName(name: string): boolean {
	if (!name || name.trim().length === 0) return false;
	if (name.length > 255) return false;

	const invalidChars = /[\\\/:*?"<>|]/;
	return !invalidChars.test(name);
}

export function generateUnicastMAC() {
	const mac = new Uint8Array(6);
	crypto.getRandomValues(mac);

	mac[0] &= 0xfe;
	mac[0] &= 0xfc;

	return Array.from(mac)
		.map((b) => b.toString(16).padStart(2, '0'))
		.join(':');
}

export function isBoolean(value: any): boolean {
	return (
		typeof value === 'boolean' ||
		(typeof value === 'string' && (value === 'true' || value === 'false'))
	);
}

export function isValidPortNumber(port: number | string): boolean {
	if (typeof port === 'string') {
		const parsed = parseInt(port, 10);
		return !isNaN(parsed) && isValidPortNumber(parsed);
	}

	return port > 0 && port < 65536;
}

export function toBase64(input: string): string {
	return btoa(String.fromCharCode(...new TextEncoder().encode(input)));
}

export function fromBase64(input: string): string {
	const decoded = atob(input);
	return new TextDecoder().decode(Uint8Array.from(decoded.split('').map((c) => c.charCodeAt(0))));
}

export function toHex(input: string): string {
	return Array.from(new TextEncoder().encode(input))
		.map((b) => b.toString(16).padStart(2, '0'))
		.join('');
}

function encodeBase62Big(num: bigint, length: number): string {
	const alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
	if (num === 0n) return '0'.repeat(length);
	let out = '';
	while (num > 0n) {
		out = alphabet[Number(num % 62n)] + out;
		num /= 62n;
	}
	return out.padStart(length, '0').slice(-length);
}

function firstHexBytes(hex: string, n: number): Uint8Array {
	const bytes = new Uint8Array(n);
	const take = Math.min(n, Math.floor(hex.length / 2));
	for (let i = 0; i < take; i++) {
		bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
	}
	return bytes;
}

export async function shortHash(input: string, rounds = 1): Promise<string> {
	const hex = await sha256(input, rounds);
	const first8 = firstHexBytes(hex, 8);
	let num = 0n;
	for (let i = 0; i < first8.length; i++) {
		num = (num << 8n) + BigInt(first8[i]);
	}
	num >>= 16n;
	return encodeBase62Big(num, 8);
}

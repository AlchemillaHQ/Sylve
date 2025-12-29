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

export function isValidRawDisk(path: string): boolean {
	if (path.endsWith('.img') || path.endsWith('.raw')) {
		return true;
	}

	return false;
}

export function getPathParent(path: string): string {
	if (typeof path !== 'string' || !path.trim()) return '';
	const parts = path.split('/').filter(Boolean);
	parts.pop();
	return '/' + parts.join('/');
}

export function isValidVMName(name: string): boolean {
	const regex = /^[a-zA-Z0-9-_]+$/;
	return regex.test(name);
}

export function isValidMACAddress(mac: string): boolean {
	return isMACAddress(mac, { no_colons: false });
}

export function isValidDUID(duid: string): boolean {
	if (typeof duid !== 'string') return false;

	const cleaned = duid.replace(/[:\s]/g, '').toLowerCase();
	if (cleaned.length < 4 || cleaned.length % 2 !== 0) return false;

	return /^[0-9a-f]+$/.test(cleaned);
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

export function parseBoolean(value: string | boolean): boolean {
	if (typeof value === 'boolean') return value;
	return value.toLowerCase() === 'true';
}

export function isValidDHCPDomain(domain: string): boolean {
	const domainRegex = /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/;
	return domainRegex.test(domain);
}

function ipToNumber(ip: string): number {
	return ip.split('.').reduce((acc, o) => acc * 256 + Number(o), 0) >>> 0;
}

function isContiguousMask(maskNum: number): boolean {
	if (maskNum === 0) return false;
	return (maskNum | (maskNum - 1)) >>> 0 === 0xffffffff;
}

export function ipMaskToCIDR(ip: string, mask: string, requireNetworkAddr = false): string | null {
	if (!isValidIPv4(ip) || !isValidIPv4(mask)) return null;

	const ipNum = ipToNumber(ip) >>> 0;
	const maskNum = ipToNumber(mask) >>> 0;

	if (!isContiguousMask(maskNum)) return null;
	if (requireNetworkAddr && (ipNum & (~maskNum >>> 0)) >>> 0 !== 0) return null;

	let cidr = 0;
	let m = maskNum;
	while (m & 0x80000000) {
		cidr++;
		m = (m << 1) >>> 0;
	}

	if (m !== 0) return null;

	return `${ip}/${cidr}`;
}

export function isValidDHCPRange(startIp: string, endIp: string): boolean {
	if (!isValidIPv4(startIp) || !isValidIPv4(endIp)) {
		return false;
	}

	const start = ipToNumber(startIp);
	const end = ipToNumber(endIp);

	if (start >= end) {
		return false;
	}

	if ((start & 0xff) === 0 || (end & 0xff) === 255) {
		return false;
	}

	return true;
}

export function isValidIPv4Range(startIP: string, endIP: string, subnet: string): boolean {
	if (!isValidIPv4(startIP) || !isValidIPv4(endIP) || !isValidIPv4(subnet)) {
		return false;
	}

	const start = ipToNumber(startIP);
	const end = ipToNumber(endIP);
	const subnetNum = ipToNumber(subnet);

	if (start >= end) {
		return false;
	}

	if ((start & 0xff) === 0 || (end & 0xff) === 255) {
		return false;
	}

	if ((start & subnetNum) !== start || (end & subnetNum) !== end) {
		return false;
	}

	return true;
}

function ipv6ToBigInt(ip: string): bigint | null {
	// Expand compressed IPv6 like "fd00::1" to 8 groups, then pack to BigInt
	const lower = ip.toLowerCase();

	// Split around "::"
	const parts = lower.split('::');
	if (parts.length > 2) return null;

	const left = parts[0] ? parts[0].split(':') : [];
	const right = parts.length === 2 && parts[1] ? parts[1].split(':') : [];

	// Validate hextets
	const isValidHextet = (h: string) => /^[0-9a-f]{1,4}$/.test(h);
	if (!left.every(isValidHextet) || !right.every(isValidHextet)) return null;

	// Pad missing groups if "::" present
	const missing = 8 - (left.length + right.length);
	if (missing < 0) return null;

	const full = parts.length === 2 ? [...left, ...Array(missing).fill('0'), ...right] : left;

	if (full.length !== 8) return null;

	// Build BigInt
	let val = 0n;
	for (const h of full) {
		val = (val << 16n) + BigInt(parseInt(h, 16));
	}
	return val;
}

function maskFromPrefix(prefix: number): bigint | null {
	if (prefix < 0 || prefix > 128) return null;
	const p = BigInt(prefix);
	// mask = ((1<<p) - 1) << (128 - p)
	return ((1n << p) - 1n) << (128n - p);
}

export function isValidIPv6Range(startIP: string, endIP: string, subnetCidr: string): boolean {
	const m = subnetCidr.split('/');
	if (m.length !== 2) return false;

	const netStr = m[0];
	const prefix = Number(m[1]);
	if (!Number.isInteger(prefix) || prefix < 0 || prefix > 128) return false;

	const start = ipv6ToBigInt(startIP);
	const end = ipv6ToBigInt(endIP);
	const net = ipv6ToBigInt(netStr);
	const mask = maskFromPrefix(prefix);

	if (start === null || end === null || net === null || mask === null) return false;
	if (start > end) return false;
	if ((net & ~mask) !== 0n) return false;

	const startNet = start & mask;
	const endNet = end & mask;
	if (startNet !== net || endNet !== net) return false;

	return true;
}

export function secondsToDnsmasq(seconds: number, turnInfiniteToZero: boolean = false): string {
	if (seconds === Infinity || seconds <= 0) {
		if (turnInfiniteToZero) {
			return '0';
		}

		return 'Infinite';
	}

	if (seconds % 86400 === 0) return `${seconds / 86400}d`;
	if (seconds % 3600 === 0) return `${seconds / 3600}h`;
	if (seconds % 60 === 0) return `${seconds / 60}m`;

	return `${seconds}`;
}

export function dnsmasqToSeconds(value: string): number {
	if (value === '0') {
		return 0;
	}

	const val = value.trim().toLowerCase();

	if (val === 'infinite') return Infinity;

	const match = val.match(/^(\d+)([smhd]?)$/);
	if (!match) throw new Error(`Invalid dnsmasq time format: ${value}`);

	const num = parseInt(match[1], 10);
	const unit = match[2] || 's';

	switch (unit) {
		case 's':
			return num;
		case 'm':
			return num * 60;
		case 'h':
			return num * 3600;
		case 'd':
			return num * 86400;
		default:
			throw new Error(`Unknown unit: ${unit}`);
	}
}

export function validateDnsmasqHostname(hostname: string): boolean {
	const regex = /^(?!.*(--|__))(?![-_])[a-zA-Z0-9-_]{1,63}(?<![-_])$/;
	return regex.test(hostname);
}

export function escapeHTML(str: string): string {
	if (!str) return '';
	return str
		.replace(/&/g, '&amp;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#39;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;');
}

export function parseNumberOrZero(value: string): number {
	const num = Number(value);
	return isNaN(num) ? 0 : num;
}

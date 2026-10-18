#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const output = resolve(process.argv[2] ?? join(root, 'live/boot/images'));
const menuOutput = join(output, 'sylve-menu');
const font = execFileSync('fc-match', ['-f', '%{file}', 'Noto Sans:style=Regular'], {
	encoding: 'utf8'
}).trim();
const title = 'FreeBSD + Sylve';
const keyboard = 'Up/Down select · Enter activate · 1–5 shortcuts';

mkdirSync(menuOutput, { recursive: true });

function text(x, y, label, size, fill, centered = false) {
	const escaped = label.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
	return `<text x="${x}" y="${y}" font-size="${size}" fill="${fill}"
		text-anchor="${centered ? 'middle' : 'start'}"
		dominant-baseline="${centered ? 'central' : 'text-before-edge'}">${escaped}</text>`;
}

function box(x, y, width, height, fill, radius = 8, outline = 'none') {
	return `<rect x="${x}" y="${y}" width="${width}" height="${height}"
		rx="${radius}" fill="${fill}" stroke="${outline}"/>`;
}

function logo(height, name = 'white.svg') {
	const png = execFileSync('magick', [
		'-background', 'none', join(root, 'ui/static/logo', name),
		'-resize', `x${height}`, 'PNG32:-'
	]);
	return { width: png.readUInt32BE(16), height, data: png.toString('base64') };
}

function image(mark, x, y, scale = 1) {
	return `<image x="${x}" y="${y}" width="${mark.width / scale}" height="${mark.height / scale}"
		xlink:href="data:image/png;base64,${mark.data}"/>`;
}

function render(width, height, content, scale = 1) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"
		width="${width * scale}" height="${height * scale}" viewBox="0 0 ${width} ${height}"
		font-family="Noto Sans" font-weight="400">${content}</svg>`;
	return execFileSync('magick', [
		'-background', 'none', 'svg:-', '-filter', 'Lanczos',
		'-resize', `${width}x${height}!`, '-strip', 'PNG32:-'
	], { input: svg });
}

const freebsdMark = logo(48, 'freebsd.svg');
const sylveMark = logo(48);
const freebsdWidth = freebsdMark.width / 2;
const sylveX = freebsdWidth + 44;
const labelX = sylveX + sylveMark.width / 2 + 10;
const labelWidth = Number(execFileSync('magick', [
	'-font', font, '-pointsize', '14', 'label:SYLVE', '-format', '%w', 'info:'
], { encoding: 'utf8' }));
const headerWidth = labelX + labelWidth + 5 * 14 * 0.35;

function header(width, y, scale = 1) {
	return `<g transform="translate(${(width - headerWidth * scale) / 2} ${y}) scale(${scale})">` +
		image(freebsdMark, 0, 0, 2) +
		text(freebsdWidth + 22, 12, '+', 18, '#999999', true) +
		image(sylveMark, sylveX, 0, 2) +
		`<text x="${labelX}" y="12" font-size="14" fill="#eeeeee"
		letter-spacing="0.35em" dominant-baseline="central">SYLVE</text></g>`;
}

writeFileSync(join(output, 'sylve-brand.png'), render(320, 96,
	box(0, 0, 320, 96, '#000000', 0) + header(320, 24) +
	text(160, 68, 'Live Installer', 14, '#999999', true), 2));

writeFileSync(join(output, 'sylve-splash.png'), render(480, 280,
	box(0, 0, 480, 280, '#000000', 0) + header(480, 88, 1.25) +
	text(240, 156, 'Starting installer...', 18, '#999999', true) +
	text(240, 188, 'Boot Options > Verbose for startup messages', 12, '#999999', true)));

const menuLogo = logo(80);

function shortcut(x, y, label, width = 30, active = false) {
	return box(x, y, width, 28, active ? '#e2e2e2' : '#222222', 6,
		active ? '#d1d1d1' : '#343434') +
		text(x + width / 2, y + 14, label, 11, active ? '#333333' : '#c7c7c7', true);
}

function menu(recovery, selected) {
	let content = box(0, 0, 1024, 768, '#0c0c0c', 0) +
		`<defs><filter id="shadow" x="-50%" y="-50%" width="200%" height="200%">
		<feGaussianBlur stdDeviation="18"/></filter></defs>
		<rect x="232" y="108" width="560" height="568" rx="18"
		fill="#000000" fill-opacity="0.47" filter="url(#shadow)"/>` +
		box(232, 100, 560, 568, '#171717', 18, '#2b2b2b') +
		image(menuLogo, 264, 132, 2) +
		text(320, 131, title, 24, '#f3f3f3') +
		text(320, 168, 'Live Installer', 13, '#949494') +
		text(264, 221, 'Choose a boot option', 13, '#a5a5a5');
	const primary = selected === 1;
	if (primary) content += box(264, 252, 496, 60, '#f0f0f0', 10);
	content += box(280, 268, 28, 28, primary ? '#dddddd' : '#242424', 6) +
		text(294, 282, '1', 13, primary ? '#303030' : '#a9a9a9', true) +
		text(324, 272, recovery ? 'Recovery shell' : 'Start installer', 18,
			primary ? '#151515' : '#e4e4e4') +
		(primary ? shortcut(684, 268, 'Enter', 58, true) : shortcut(712, 268, recovery ? 'S' : 'B'));

	const alternate = recovery
		? ['Start installer', 'Continue to the setup wizard', 'B']
		: ['Recovery shell', 'Troubleshooting and manual recovery', 'S'];
	const items = [
		[2, 338, ...alternate, 30],
		[3, 408, 'Boot options', 'Verbose output, safe mode and console settings', 'O', 30],
		[4, 478, 'Restart', 'Restart this machine', 'R', 30],
		[5, 548, 'Loader prompt', 'Open the FreeBSD loader command line', 'Esc', 42]
	];
	for (const [number, y, label, description, key, width] of items) {
		const active = selected === number;
		if (active) content += box(264, y - 12, 496, 60, '#f0f0f0', 10);
		content += box(280, y + 4, 28, 28, active ? '#dddddd' : '#242424', 6) +
			text(294, y + 18, String(number), 13, active ? '#303030' : '#a9a9a9', true) +
			text(324, y, label, 17, active ? '#151515' : '#e4e4e4') +
			text(324, y + 29, description, 12, active ? '#666666' : '#909090') +
			(active ? shortcut(684, y + 4, 'Enter', 58, true) : shortcut(742 - width, y + 4, key, width));
	}
	content += '<path d="M264 614H760" stroke="#2b2b2b"/>' +
		text(264, 635, keyboard, 12, '#929292');
	return render(1024, 768, content, 2);
}

for (const recovery of [false, true]) {
	const name = recovery ? 'recovery' : 'main';
	const states = Array.from({ length: 5 }, (_, index) => menu(recovery, index + 1));
	writeFileSync(join(menuOutput, `${name}.png`), states[0]);
	for (const [index, y] of [248, 322, 392, 462, 532].entries()) {
		const normal = states[index === 0 ? 1 : 0];
		for (const [suffix, state] of [['', normal], ['-selected', states[index]]]) {
			const row = execFileSync('magick', [
				'png:-', '-crop', `504x68+260+${y}`, '+repage', '-strip', 'PNG32:-'
			], { input: state });
			writeFileSync(join(menuOutput, `${name}-row-${index + 1}${suffix}.png`), row);
		}
	}
}

function footer(name, label) {
	writeFileSync(join(menuOutput, `${name}.png`), render(496, 32,
		box(0, 0, 496, 32, '#171717', 0) + text(0, 11, label, 12, '#929292'), 2));
}

footer('keyboard', keyboard);
footer('autoboot', 'Automatic start · Space to pause');
for (let seconds = 0; seconds <= 15; seconds++) {
	footer(`countdown-${seconds}`, `Starting in ${seconds}s · Space to pause`);
}

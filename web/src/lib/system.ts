import { storage } from "$lib";

export const isMac = navigator.userAgent.includes('Mac');
export const cmdOrCtrl = isMac ? '⌘' : 'Ctrl';
export const optionOrAlt = isMac ? '⌥' : 'Alt';

export function handleCommandKeydown(e: KeyboardEvent) {
    if (e.repeat) return;
    if (e.key === '/' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        storage.openCommands = !storage.openCommands;
    }
}
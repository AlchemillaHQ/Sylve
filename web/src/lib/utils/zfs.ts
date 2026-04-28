/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type {
    BackupRestoreGenerationOption,
    BackupSnapshotLineageMarker,
    BackupTarget,
    BackupTargetDatasetInfo,
    SnapshotInfo
} from '$lib/types/cluster/backups';

export function isValidPoolName(name: string): boolean {
    const reserved = ['log', 'mirror', 'raidz', 'raidz1', 'raidz2', 'raidz3', 'spare'];

    if (!name) return false;
    if (reserved.some((r) => name.startsWith(r))) return false;
    if (!/^[a-zA-Z]/.test(name)) return false;
    if (!/^[a-zA-Z0-9_.-]+$/.test(name)) return false;
    if (name.includes('%')) return false;
    if (/^c[0-9]/.test(name)) return false;

    return true;
}

export function isValidDatasetName(name: string): boolean {
    if (!name || typeof name !== 'string') return false;
    if (name.length > 255) return false;
    if (/[^\x21-\x7E]/.test(name)) return false;
    if (name.includes('%') || name.includes(' ')) return false;

    const components = name.split('/');
    for (const comp of components) {
        if (!comp) return false;
        if (!/^[a-zA-Z0-9_.-]+$/.test(comp)) return false;
        if (comp.startsWith('.') || comp.startsWith('-')) return false;
    }

    return true;
}

export function roundUpToBlock(size: number, block: number): number {
    return Math.ceil(size / block) * block;
}

export function datasetLineageRank(lineage: string): number {
    switch (lineage || 'active') {
        case 'active':
            return 0;
        case 'rotated':
            return 1;
        case 'preserved':
            return 2;
        default:
            return 3;
    }
}

export function pickRepresentativeDataset(
    datasets: BackupTargetDatasetInfo[]
): BackupTargetDatasetInfo | null {
    if (datasets.length === 0) return null;
    const ranked = [...datasets].sort((left, right) => {
        const rankDiff =
            datasetLineageRank(left.lineage || 'active') - datasetLineageRank(right.lineage || 'active');
        if (rankDiff !== 0) return rankDiff;
        if ((left.snapshotCount || 0) !== (right.snapshotCount || 0)) {
            return (right.snapshotCount || 0) - (left.snapshotCount || 0);
        }
        return left.name.localeCompare(right.name);
    });
    return ranked[0];
}

export function formatLineageLabel(lineage: string, outOfBand: boolean): string {
    switch (lineage) {
        case 'active':
            return 'Current';
        case 'rotated':
            return 'OOB lineage';
        case 'preserved':
            return 'System preserved';
        default:
            return outOfBand ? 'Out of band' : 'Current';
    }
}

export function snapshotLineageLabel(snapshot: SnapshotInfo): string {
    return formatLineageLabel(snapshot.lineage || 'active', !!snapshot.outOfBand);
}

export function formatRestoreSnapshotDate(snapshot: SnapshotInfo): string {
    if (!snapshot.creation) return '-';
    const date = new Date(snapshot.creation);
    if (Number.isNaN(date.getTime())) {
        return snapshot.creation;
    }
    return date.toLocaleString();
}

export function inferJailDestinationDataset(
    target: BackupTarget | undefined,
    dataset: string
): string {
    if (!target) return '';
    const jailMatch = dataset.match(/(?:^|\/)jails\/(\d+)(?:$|\/)/);
    if (!jailMatch) return '';
    const ctid = jailMatch[1];
    const pool = target.backupRoot.split('/')[0] || '';
    if (!pool) return '';
    return `${pool}/sylve/jails/${ctid}`;
}

export function inferVMDestinationDataset(
    target: BackupTarget | undefined,
    dataset: string
): string {
    if (!target) return '';
    const vmMatch = dataset.match(/(?:^|\/)virtual-machines\/(\d+)(?:$|\/)/);
    if (!vmMatch) return '';
    const rid = vmMatch[1];

    let pool = '';
    const datasetPoolMatch = dataset.match(/(?:^|\/)([^/]+)\/sylve\/virtual-machines\/\d+(?:$|\/)/);
    if (datasetPoolMatch) {
        pool = datasetPoolMatch[1];
    }
    if (!pool) {
        pool = target.backupRoot.split('/')[0] || '';
    }
    if (!pool) return '';

    return `${pool}/sylve/virtual-machines/${rid}`;
}

// ---------------------------------------------------------------------------
// Snapshot generation / lineage helpers (shared between Restore and OOBRestore)
// ---------------------------------------------------------------------------

export function snapshotLineageMarker(item: SnapshotInfo): BackupSnapshotLineageMarker {
    const lineage = item.lineage || 'active';
    if (lineage === 'preserved') return 'INT';
    if (lineage === 'active' && !item.outOfBand) return 'CURR';
    return 'OOB';
}

export function snapshotGenerationTag(item: SnapshotInfo): string {
    const datasetName =
        (item.dataset && item.dataset.trim()) ||
        (item.name.includes('@') ? item.name.slice(0, item.name.lastIndexOf('@')) : '');
    if (!datasetName) return '';
    const leaf = datasetName.slice(datasetName.lastIndexOf('/') + 1);
    if (!leaf) return '';
    if (leaf === 'active') return 'active';
    const marker = leaf.match(/(?:^|_)((?:bk|zelta)_[0-9a-z._-]+|gen-[0-9a-z._-]+)$/i);
    if (marker) return marker[1];
    return leaf;
}

export function parseGenerationTimestampMs(tag: string): number | null {
    const trimmed = (tag || '').trim().toLowerCase();
    if (!trimmed || trimmed === 'active') return null;
    const token = trimmed.startsWith('gen-')
        ? trimmed.slice(4)
        : trimmed.startsWith('bk_')
            ? trimmed.slice(3)
            : trimmed.startsWith('zelta_')
                ? trimmed.slice(6)
                : '';
    if (!token) return null;
    const parts = token.split('_');
    const candidate = parts.length > 1 ? parts[parts.length - 1] : token;
    if (!candidate) return null;
    const parsed = Number.parseInt(candidate, 36);
    if (!Number.isFinite(parsed) || Number.isNaN(parsed) || parsed <= 0) return null;
    return parsed;
}

export function parseSnapshotTimeMs(item: SnapshotInfo): number | null {
    const raw = (item.creation || '').trim();
    if (!raw) return null;
    const ms = Date.parse(raw);
    if (!Number.isFinite(ms) || Number.isNaN(ms)) return null;
    return ms;
}

export function snapshotGenerationKey(item: SnapshotInfo): string {
    const lineage = item.lineage || 'active';
    if (lineage === 'active' && !item.outOfBand) return 'active';
    const generation = snapshotGenerationTag(item);
    return generation && generation.trim() !== '' ? generation : 'active';
}

export function generationLabelFromKey(key: string, aliasByTag: Map<string, string>): string {
    if ((key || '').trim() === '' || key === 'active') return 'Current';
    return aliasByTag.get(key) || key;
}

export function buildGenerationAliasMap(items: SnapshotInfo[]): Map<string, string> {
    const generationTime = new Map<string, number>();
    for (const item of items) {
        const generation = snapshotGenerationTag(item);
        if (!generation || generation === 'active') continue;
        const generationMs = parseGenerationTimestampMs(generation);
        const snapshotMs = parseSnapshotTimeMs(item);
        const inferredMs = generationMs ?? snapshotMs ?? Number.MAX_SAFE_INTEGER;
        const existing = generationTime.get(generation);
        if (existing === undefined || inferredMs < existing) {
            generationTime.set(generation, inferredMs);
        }
    }
    const ordered = [...generationTime.entries()].sort((left, right) => {
        if (left[1] !== right[1]) return left[1] - right[1];
        return left[0].localeCompare(right[0]);
    });
    const aliases = new Map<string, string>();
    for (let index = 0; index < ordered.length; index++) {
        aliases.set(ordered[index][0], `gen-${index + 1}`);
    }
    return aliases;
}

export function filterSnapshotsByGeneration(items: SnapshotInfo[], generation: string): SnapshotInfo[] {
    const target = (generation || '').trim();
    if (!target) return items;
    return items.filter((item) => snapshotGenerationKey(item) === target);
}

export function buildGenerationOptions(
    items: SnapshotInfo[],
    aliasByTag: Map<string, string>
): BackupRestoreGenerationOption[] {
    if (items.length === 0) return [];
    const groups = new Map<string, { count: number; sortMs: number }>();
    for (const item of items) {
        const key = snapshotGenerationKey(item);
        const generationMs = parseGenerationTimestampMs(key);
        const snapshotMs = parseSnapshotTimeMs(item);
        const inferredMs = generationMs ?? snapshotMs ?? Number.MAX_SAFE_INTEGER;
        const existing = groups.get(key);
        if (!existing) {
            groups.set(key, { count: 1, sortMs: inferredMs });
            continue;
        }
        existing.count += 1;
        if (inferredMs < existing.sortMs) existing.sortMs = inferredMs;
    }
    const ordered = [...groups.entries()].sort((left, right) => {
        const lk = left[0];
        const rk = right[0];
        if (lk === 'active' && rk !== 'active') return -1;
        if (rk === 'active' && lk !== 'active') return 1;
        if (left[1].sortMs !== right[1].sortMs) return left[1].sortMs - right[1].sortMs;
        return lk.localeCompare(rk);
    });
    return ordered.map(([key, meta]) => ({
        value: key,
        label: `${generationLabelFromKey(key, aliasByTag)} (${meta.count})`
    }));
}

/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

import type { BackupTarget, BackupTargetDatasetInfo, SnapshotInfo } from "$lib/types/cluster/backups";

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
            datasetLineageRank(left.lineage || 'active') -
            datasetLineageRank(right.lineage || 'active');
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

export function inferJailDestinationDataset(target: BackupTarget | undefined, dataset: string): string {
    if (!target) return '';
    const jailMatch = dataset.match(/(?:^|\/)jails\/(\d+)(?:$|\/)/);
    if (!jailMatch) return '';
    const ctid = jailMatch[1];
    const pool = target.backupRoot.split('/')[0] || '';
    if (!pool) return '';
    return `${pool}/sylve/jails/${ctid}`;
}

export function inferVMDestinationDataset(target: BackupTarget | undefined, dataset: string): string {
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
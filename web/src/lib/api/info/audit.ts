import { AuditRecordSchema, type AuditRecord } from '$lib/types/info/audit';
import { apiRequest } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getAuditRecords(hostname?: string): Promise<AuditRecord[]> {
    return await apiRequest('/info/audit-records', z.array(AuditRecordSchema), 'GET', undefined, {
        hostname
    });
}

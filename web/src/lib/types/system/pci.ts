import { z } from 'zod/v4';
import type { Row } from '../components/tree-table';

export const PCIDeviceSchema = z.object({
	name: z.string(),
	unit: z.number(),
	domain: z.number(),
	bus: z.number(),
	device: z.number(),
	function: z.number(),
	class: z.number().int(),
	rev: z.number().int(),
	hdr: z.number().int(),
	vendor: z.number().int(),
	subVendor: z.number().int().optional().default(0),
	subDevice: z.number().int().optional().default(0),
	names: z.object({
		vendor: z.string().optional().default(''),
		device: z.string().optional().default(''),
		class: z.string().optional().default(''),
		subclass: z.string().optional().default('')
	})
});

export const PPTDeviceSchema = z.object({
	id: z.number().int().positive(),
	domain: z.number().int(),
	oldDriver: z.string(),
	deviceID: z.string()
});

export type DeviceRow = Row & {
	name: string;
	domain: number;
	deviceId: string;
	pptId?: number | string;
};

export type PCIDevice = z.infer<typeof PCIDeviceSchema>;
export type PPTDevice = z.infer<typeof PPTDeviceSchema>;

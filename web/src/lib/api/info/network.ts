import {
	HistoricalNetworkInterfaceSchema,
	type HistoricalNetworkInterface
} from '$lib/types/info/network';
import { apiRequestData } from '$lib/utils/http';
import { z } from 'zod/v4';

export async function getNetworkInterfaceInfoHistorical(): Promise<HistoricalNetworkInterface[]> {
	return await apiRequestData(
		'/info/network-interfaces/historical',
		z.array(HistoricalNetworkInterfaceSchema),
		'GET'
	);
}

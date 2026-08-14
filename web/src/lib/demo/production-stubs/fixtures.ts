type ProductionRequestConfig = {
	url: string;
	method?: string;
};

type ProductionClientResponse<T> = {
	status: number;
	data: T;
	headers: Record<string, string>;
	ok: boolean;
};

export async function handleDemoRequest<T = unknown>(
	_config: ProductionRequestConfig
): Promise<ProductionClientResponse<T>> {
	throw new Error('Demo fixtures are not part of the production build.');
}

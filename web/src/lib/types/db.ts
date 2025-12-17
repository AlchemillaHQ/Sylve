export type KVEntry<T = unknown> = {
	key: string;
	value: T;
	timestamp: number;
};

import { deepDiff } from './obj';

export function createEmptyArrayOfArrays(length: number): Array<Array<any>> {
	return Array.from({ length }, () => []);
}

export function deepDiffArr(arr1: any[], arr2: any[]): any[] {
	const changes = [];

	for (let i = 0; i < Math.max(arr1.length, arr2.length); i++) {
		const val1 = arr1[i];
		const val2 = arr2[i];

		if (typeof val1 === 'object' && typeof val2 === 'object' && val1 && val2) {
			changes.push(...deepDiff(val1, val2, `${i}`));
		} else if (val1 !== val2) {
			changes.push({ path: `${i}`, from: val1, to: val2 });
		}
	}

	return changes;
}

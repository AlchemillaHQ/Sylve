export function compareMACs(mac1: string, mac2: string, ignoreFirstByte = false) {
  const clean = (m: string) => m.toLowerCase().replace(/[^a-f0-9]/g, '');
  let [m1, m2] = [clean(mac1), clean(mac2)];

  if (m1.length !== 12 || m2.length !== 12) {
    throw new Error('Invalid MAC address format');
  }

  if (ignoreFirstByte) {
    m1 = m1.slice(2);
    m2 = m2.slice(2);
  }

  const n1 = BigInt('0x' + m1);
  const n2 = BigInt('0x' + m2);

  if (n1 === n2) return 0;   // equal
  if (n2 === n1 + 1n) return +1; // mac2 is +1
  if (n2 === n1 - 1n) return -1; // mac2 is -1
  return -2; // not equal or ±1
}

export const isMACNearOrEqual = (a?: string, b?: string, ignoreFirstByte = false): boolean => {
  if (!a || !b) return false;
  try {
    return Math.abs(compareMACs(a, b, ignoreFirstByte)) <= 1;
  } catch {
    return false;
  }
};
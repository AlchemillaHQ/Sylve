import type { Iface } from "$lib/types/network/iface";
import type { SwitchList } from "$lib/types/network/switch";

export function getFriendlyName(i: string, switches: SwitchList, interfaces: Iface[]): string {
    if (!i) return i;
    const stdSwitch = switches.standard?.find((sw) => sw.bridgeName === i);
    if (stdSwitch) return stdSwitch.name;
    const manSwitch = switches.manual?.find((sw) => sw.bridge === i);
    if (manSwitch) return manSwitch.name;
    const iface = interfaces.find((iface) => iface.name === i);
    if (iface?.description) return iface.description;
    return i;
}
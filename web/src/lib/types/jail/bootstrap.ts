export interface BootstrapEntry {
    pool: string;
    name: string;
    label: string;
    dataset: string;
    mountPoint: string;
    major: number;
    minor: number;
    type: string;
    exists: boolean;
    status: string;
    phase: string;
    error: string;
}

export interface BootstrapRequest {
    pool: string;
    major: number;
    minor: number;
    type: string;
}

export interface SupportedBootstrapVersion {
    major: number;
    minor: number;
}

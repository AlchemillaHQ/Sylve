export type DemoHostTerminalStatus = 'idle' | 'loading' | 'running' | 'error';

type DemoHostTerminalSnapshot = {
	status: DemoHostTerminalStatus;
	text: string;
	progress: number;
};

const unavailable: DemoHostTerminalSnapshot = {
	status: 'idle',
	text: '',
	progress: 0
};

export const demoHostTerminal = {
	attach: () => () => {},
	refresh: () => {},
	resize: () => {},
	send: () => {},
	setHostname: () => {},
	subscribe: (listener: (snapshot: DemoHostTerminalSnapshot) => void) => {
		listener(unavailable);
		return () => {};
	}
};

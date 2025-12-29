type RefetchCallback = () => void;

const focusSubscribers = new Set<RefetchCallback>();
const onlineSubscribers = new Set<RefetchCallback>();

let listenersInitialized = false;

function ensureListeners() {
	if (listenersInitialized) return;
	if (typeof window === 'undefined' || typeof document === 'undefined') return;

	listenersInitialized = true;

	const handleFocusOrVisible = () => {
		if (document.visibilityState !== 'visible') return;
		for (const cb of focusSubscribers) cb();
	};

	window.addEventListener('focus', handleFocusOrVisible);
	document.addEventListener('visibilitychange', handleFocusOrVisible);

	window.addEventListener('online', () => {
		for (const cb of onlineSubscribers) cb();
	});
}

export function subscribeToFocus(cb: RefetchCallback): () => void {
	ensureListeners();
	focusSubscribers.add(cb);
	return () => {
		focusSubscribers.delete(cb);
	};
}

export function subscribeToOnline(cb: RefetchCallback): () => void {
	ensureListeners();
	onlineSubscribers.add(cb);
	return () => {
		onlineSubscribers.delete(cb);
	};
}

import { PersistedState } from 'runed';

interface TerminalTab {
    id: string;
    title: string;
}

class HostTerminalStore {
    // Persist the list of tabs and the active ID to localStorage
    state = new PersistedState<{
        tabs: TerminalTab[];
        activeTabId: string | null;
        isOpen: boolean;
    }>('sylve-host-terminal-state', {
        tabs: [{ id: 'default', title: 'Main Shell' }],
        activeTabId: 'default',
        isOpen: false
    });

    get tabs() { return this.state.current.tabs; }
    set tabs(v) { this.state.current.tabs = v; }

    get activeTabId() { return this.state.current.activeTabId; }
    set activeTabId(v) { this.state.current.activeTabId = v; }

    get isOpen() { return this.state.current.isOpen; }
    set isOpen(v) { this.state.current.isOpen = v; }

    addTab() {
        const id = `host-${Math.random().toString(36).substring(2, 9)}`;
        const newTab = { id, title: `Shell ${this.tabs.length + 1}` };
        this.tabs = [...this.tabs, newTab];
        this.activeTabId = id;
    }

    closeTab(id: string) {
        this.tabs = this.tabs.filter(t => t.id !== id);
        if (this.activeTabId === id && this.tabs.length > 0) {
            this.activeTabId = this.tabs[0].id;
        }
    }
}

export const hostStore = new HostTerminalStore();
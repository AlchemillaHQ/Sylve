/**
 * SPDX-License-Identifier: BSD-2-Clause
 *
 * Copyright (c) 2025 The FreeBSD Foundation.
 *
 * This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
 * of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
 * under sponsorship from the FreeBSD Foundation.
 */

export function draggable(node: HTMLElement, data: string) {
	let state = data;

	node.draggable = true;
	node.style.cursor = 'grab';

	function handle_dragstart(e: DragEvent) {
		if (!e.dataTransfer) return;
		//const dataToTransfer = typeof state === 'string' ? state : state.toString();
		e.dataTransfer.setData('application/disk', state);
		e.dataTransfer.setData('text/plain', state);
		// Add this to make the drag image transparent if desired
		// setTimeout(() => {
		//     node.style.opacity = '0.4';
		// }, 0);
	}

	function handle_dragend(e: DragEvent) {
		// Reset any visual changes
		// node.style.opacity = '1';
	}

	node.addEventListener('dragstart', handle_dragstart);
	node.addEventListener('dragend', handle_dragend);

	return {
		update(data: string) {
			state = data;
		},

		destroy() {
			node.removeEventListener('dragstart', handle_dragstart);
			node.removeEventListener('dragend', handle_dragend);
		}
	};
}

export function dropzone(
	node: HTMLElement,
	options: {
		dropEffect?: 'move' | 'none' | 'copy' | 'link';
		dragover_class?: string;
		on_dropzone?: (data: string, e: DragEvent) => void;
	}
) {
	let state = {
		dropEffect: 'move' as 'move' | 'none' | 'copy' | 'link',
		dragover_class: 'droppable',
		...options
	};

	let dragCounter = 0;

	function handle_dragenter(e: DragEvent) {
		e.preventDefault();
		dragCounter++;

		if (dragCounter === 1) {
			node.classList.add(state.dragover_class);
		}
	}

	function handle_dragleave(e: DragEvent) {
		dragCounter--;

		if (dragCounter === 0) {
			// Only remove class when fully leaving
			node.classList.remove(state.dragover_class);
		}
	}

	function handle_dragover(e: DragEvent) {
		e.preventDefault();
		if (!e.dataTransfer) return;
		e.dataTransfer.dropEffect = state.dropEffect;
	}

	function handle_drop(e: DragEvent) {
		e.preventDefault();
		e.stopPropagation();

		dragCounter = 0;
		node.classList.remove(state.dragover_class);

		if (!e.dataTransfer) return;
		const data = e.dataTransfer.getData('text/plain');

		if (typeof state.on_dropzone === 'function') {
			state.on_dropzone(data, e);
		}
	}

	node.addEventListener('dragenter', handle_dragenter);
	node.addEventListener('dragleave', handle_dragleave);
	node.addEventListener('dragover', handle_dragover);
	node.addEventListener('drop', handle_drop);

	return {
		update(options: {
			dropEffect?: 'move' | 'none' | 'copy' | 'link';
			dragover_class?: string;
			on_dropzone?: (data: string, e: DragEvent) => void;
		}) {
			state = {
				dropEffect: 'move',
				dragover_class: 'droppable',
				...options
			};
		},

		destroy() {
			node.removeEventListener('dragenter', handle_dragenter);
			node.removeEventListener('dragleave', handle_dragleave);
			node.removeEventListener('dragover', handle_dragover);
			node.removeEventListener('drop', handle_drop);
		}
	};
}

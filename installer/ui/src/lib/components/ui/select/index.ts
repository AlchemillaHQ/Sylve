import { Select as SelectPrimitive } from 'bits-ui';

import Item from './select-item.svelte';
import Content from './select-content.svelte';
import Trigger from './select-trigger.svelte';
import ScrollDownButton from './select-scroll-down-button.svelte';
import ScrollUpButton from './select-scroll-up-button.svelte';

const Root = SelectPrimitive.Root;

export {
	Root,
	Item,
	Content,
	Trigger,
	ScrollDownButton,
	ScrollUpButton,
	Root as Select,
	Item as SelectItem,
	Content as SelectContent,
	Trigger as SelectTrigger,
	ScrollDownButton as SelectScrollDownButton,
	ScrollUpButton as SelectScrollUpButton
};

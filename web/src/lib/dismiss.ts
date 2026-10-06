/**
 * Attaches to a <details> menu so a click outside it or Escape closes it, the way menus usually
 * behave. Escape puts focus back on the menu's button.
 */
export function dismiss(node: HTMLDetailsElement): () => void {
	const outside = (e: PointerEvent) => {
		if (node.open && !node.contains(e.target as Node)) node.open = false;
	};
	const escape = (e: KeyboardEvent) => {
		if (node.open && e.key === 'Escape') {
			node.open = false;
			node.querySelector('summary')?.focus();
		}
	};
	document.addEventListener('pointerdown', outside);
	document.addEventListener('keydown', escape);
	return () => {
		document.removeEventListener('pointerdown', outside);
		document.removeEventListener('keydown', escape);
	};
}

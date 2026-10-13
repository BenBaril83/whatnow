import type { Theme } from './api';

/** The looks Settings offers, the default first. */
export const themes: { id: Theme; name: string }[] = [
	{ id: '', name: 'Sticker' },
	{ id: 'plain', name: 'Dream of the 90s (plain)' },
	{ id: 'neon', name: 'Neon Nights' },
	{ id: 'terminal', name: 'Terminal' },
	{ id: 'paper', name: 'Paper' },
	{ id: 'vaporwave', name: 'Vaporwave' }
];

/** Where the theme is mirrored, so app.html can set it before the first paint. */
export const storedThemeKey = 'whatnow-theme';

/**
 * Puts a theme on the page. An unknown theme gets the sticker look. Storage can be blocked, and
 * the page themes itself regardless.
 */
export function applyTheme(
	theme: string,
	root: { dataset: DOMStringMap } = document.documentElement,
	storage: Pick<Storage, 'setItem' | 'removeItem'> = localStorage
): void {
	const known = themes.some((t) => t.id === theme) && theme !== '';
	if (known) root.dataset.theme = theme;
	else delete root.dataset.theme;
	try {
		if (known) storage.setItem(storedThemeKey, theme);
		else storage.removeItem(storedThemeKey);
	} catch {
		// The mirror only saves a flash of the sticker look on load.
	}
}

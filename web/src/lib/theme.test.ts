import { describe, expect, it } from 'vitest';
import { applyTheme, storedThemeKey } from './theme';

function page(throws = false) {
	const root = { dataset: {} as DOMStringMap };
	const saved = new Map<string, string>();
	const storage = {
		setItem: (k: string, v: string) => {
			if (throws) throw new Error('blocked');
			saved.set(k, v);
		},
		removeItem: (k: string) => {
			if (throws) throw new Error('blocked');
			saved.delete(k);
		}
	};
	return { root, saved, storage };
}

describe('applyTheme', () => {
	it('Should set the theme on the page and remember it for the next load', () => {
		const p = page();
		applyTheme('plain', p.root, p.storage);
		expect(p.root.dataset.theme).toBe('plain');
		expect(p.saved.get(storedThemeKey)).toBe('plain');
	});

	it.each([
		['the default', ''],
		['an unknown theme', 'geocities']
	])('Should fall back to the sticker look for %s', (_, theme) => {
		const p = page();
		applyTheme('neon', p.root, p.storage);
		applyTheme(theme, p.root, p.storage);
		expect(p.root.dataset.theme).toBeUndefined();
		expect(p.saved.has(storedThemeKey)).toBe(false);
	});

	it('Should still theme the page when storage is blocked', () => {
		const p = page(true);
		applyTheme('terminal', p.root, p.storage);
		expect(p.root.dataset.theme).toBe('terminal');
	});
});

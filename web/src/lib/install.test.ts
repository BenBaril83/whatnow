import { describe, expect, it } from 'vitest';
import { installBannerDue, installPlatform } from '#lib/install.ts';

describe('installBannerDue', () => {
	it.each([
		[true, false, false, true],
		[false, false, false, false],
		[true, true, false, false],
		[true, false, true, false],
	])(
		'Should be due for hosted=%s standalone=%s dismissed=%s: %s',
		(hosted, standalone, dismissed, want) => {
			expect(installBannerDue(hosted, standalone, dismissed)).toBe(want);
		},
	);
});

describe('installPlatform', () => {
	it.each([
		[
			'Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1',
			'ios',
		],
		[
			'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Mobile Safari/537.36',
			'android',
		],
		[
			'Mozilla/5.0 (X11; Linux x86_64; rv:125.0) Gecko/20100101 Firefox/125.0',
			'other',
		],
	])('Should classify %s', (ua, want) => {
		expect(installPlatform(ua)).toBe(want);
	});
});

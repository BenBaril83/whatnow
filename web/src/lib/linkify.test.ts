import { describe, expect, it } from 'vitest';
import { linkify } from './linkify';

describe('linkify', () => {
	it('Should split links out of the text around them', () => {
		expect(linkify('PR at https://github.com/x/y/pull/4 then deploy')).toEqual([
			{ text: 'PR at ' },
			{ text: 'https://github.com/x/y/pull/4', href: 'https://github.com/x/y/pull/4' },
			{ text: ' then deploy' }
		]);
	});

	it('Should leave closing punctuation out of a link', () => {
		expect(linkify('see (http://a.com/b). Or https://c.com/d?e=1, fine')).toEqual([
			{ text: 'see (' },
			{ text: 'http://a.com/b', href: 'http://a.com/b' },
			{ text: '). Or ' },
			{ text: 'https://c.com/d?e=1', href: 'https://c.com/d?e=1' },
			{ text: ', fine' }
		]);
	});

	it('Should keep text with no links whole, and only link http and https', () => {
		expect(linkify('call mom, ftp://x.com or javascript:alert(1)')).toEqual([
			{ text: 'call mom, ftp://x.com or javascript:alert(1)' }
		]);
	});
});

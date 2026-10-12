/** A run of text, and its address when the run is a link. */
export interface Segment {
	text: string;
	href?: string;
}

// A link runs to the next space; punctuation that ends a sentence or closes a bracket isn't part of it.
const url = /https?:\/\/\S+?(?=[.,;:!?)\]'"]*(?:\s|$))/g;

/** Splits text into plain runs and http(s) links, in order. */
export function linkify(text: string): Segment[] {
	const out: Segment[] = [];
	let at = 0;
	for (const m of text.matchAll(url)) {
		if (m.index > at) out.push({ text: text.slice(at, m.index) });
		out.push({ text: m[0], href: m[0] });
		at = m.index + m[0].length;
	}
	if (at < text.length) out.push({ text: text.slice(at) });
	return out;
}

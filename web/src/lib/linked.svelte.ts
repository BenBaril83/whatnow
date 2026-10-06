/** The card whose blocker links are showing, and the tasks they point at, so those can light up. */
class Linked {
	source = $state<number | null>(null);
	ids = $state<number[]>([]);

	/** Shows a card's links, or hides them if they're already showing. */
	toggle(source: number, ids: number[]): void {
		const showing = this.source === source;
		this.source = showing ? null : source;
		this.ids = showing ? [] : ids;
	}
}

export const linked = new Linked();

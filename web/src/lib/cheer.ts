import confetti from 'canvas-confetti';
import type { Celebration } from './celebrate';

const tokens = ['--primary', '--now', '--chip', '--should', '--event', '--must'];

/** Throws the confetti a celebration earns, from the finished task's card when it's on screen. */
export function cheer(size: Celebration, taskIds: number[]): void {
	const css = getComputedStyle(document.documentElement);
	const colors = tokens.map((t) => css.getPropertyValue(t).trim());
	const fire = (opts: confetti.Options) => confetti({ colors, disableForReducedMotion: true, ...opts });

	if (size === 'finale') {
		const end = Date.now() + 2500;
		const burst = () => {
			fire({ particleCount: 60, spread: 360, startVelocity: 30, ticks: 90, origin: { x: Math.random(), y: Math.random() * 0.5 } });
			if (Date.now() < end) setTimeout(burst, 350);
		};
		burst();
		return;
	}
	const card = taskIds.map((id) => document.querySelector(`[data-task="${id}"]`)).find((el) => el);
	const box = card?.getBoundingClientRect();
	const origin = box
		? { x: (box.left + 24) / innerWidth, y: (box.top + box.height / 2) / innerHeight }
		: { x: 0.5, y: 0.7 };
	if (size === 'must') fire({ particleCount: 120, spread: 100, startVelocity: 40, origin });
	else fire({ particleCount: 40, spread: 60, startVelocity: 25, ticks: 120, scalar: 0.8, origin });
}

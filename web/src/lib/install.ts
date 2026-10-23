export const installDismissedKey = 'whatnow.install-dismissed';

/** The banner is for hosted visitors who haven't installed the app or waved it off. */
export function installBannerDue(
	hosted: boolean,
	standalone: boolean,
	dismissed: boolean,
): boolean {
	return hosted && !standalone && !dismissed;
}

/** Which home-screen steps to show; iPadOS Safari reports a Mac, so only iPhone and iPad strings count. */
export function installPlatform(userAgent: string): 'ios' | 'android' | 'other' {
	if (/iPhone|iPad|iPod/.test(userAgent)) return 'ios';
	if (/Android/.test(userAgent)) return 'android';
	return 'other';
}

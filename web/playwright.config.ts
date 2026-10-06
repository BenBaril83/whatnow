import { defineConfig, devices } from '@playwright/test';

// `just e2e` builds ../whatnow-e2e: the real server, with the fake model and an in-memory keychain.
const addr = '127.0.0.1:7788';

export default defineConfig({
	testDir: 'e2e',
	// One server and one database: tests take turns, each starting from a wiped install.
	workers: 1,
	use: { baseURL: `http://${addr}`, trace: 'retain-on-failure' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
	webServer: {
		command: `../whatnow-e2e serve -addr ${addr} -db "$(mktemp -d)/whatnow.db"`,
		url: `http://${addr}/api/v1/settings`
	}
});

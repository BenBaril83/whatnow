import { defineConfig, devices } from '@playwright/test';

// The server is the real one built with the e2e tag: the fake model and an in-memory keychain. It
// is rebuilt on every run, because an untagged binary would wipe the real keychain's whatnow keys.
const addr = '127.0.0.1:7788';
// The Linear spec's fake listens here, because the server needs the address before any test runs.
const linearAddr = '127.0.0.1:7790';

export default defineConfig({
	testDir: 'e2e',
	// One server and one database: tests take turns, each starting from a wiped install.
	workers: 1,
	use: { baseURL: `http://${addr}`, trace: 'retain-on-failure' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
	webServer: {
		command: `cd .. && export WHATNOW_E2E_LINEAR_URL=http://${linearAddr} && go build -tags e2e -o whatnow-e2e ./cmd/whatnow && exec ./whatnow-e2e serve -addr ${addr} -db "$(mktemp -d)/whatnow.db"`,
		url: `http://${addr}/api/v1/settings`
	}
});

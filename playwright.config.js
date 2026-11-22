// Browser test seam: Playwright drives a freshly built binary in headless
// Chromium. See AGENTS.md ("Browser tests") for the commands.
const { defineConfig, devices } = require("@playwright/test");

const port = Number(process.env.BROWSER_TEST_PORT || 8598);

module.exports = defineConfig({
  testDir: "browser-tests",
  // Every test shares one database and one server: run them one at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: true,
  timeout: 30_000,
  reporter: "list",
  outputDir: "test-results",
  use: {
    baseURL: `http://localhost:${port}`,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash browser-tests/serve.sh",
    url: `http://localhost:${port}/api/health`,
    env: { BROWSER_TEST_PORT: String(port) },
    // Never reuse whatever already listens on the port: it may be a deployed
    // instance running different code against a different database.
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: "ignore",
    stderr: "pipe",
  },
});

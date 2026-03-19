const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");

const pkg = require("../package.json");
const cliName = pkg.config?.cliBinaryName || "procoder";
const {
  buildFallbackBuilds,
  buildInstallPlan,
  helperAssetName,
  resolveHostTarget
} = require("./postinstall.js");

test("buildInstallPlan uses the host procoder asset and packaged helper asset", () => {
  const rootDir = "/tmp/procoder";
  const plan = buildInstallPlan({
    pkg,
    cliName,
    platform: "darwin",
    arch: "x64",
    rootDir
  });

  assert.deepEqual(plan.hostTarget, { goos: "darwin", goarch: "amd64" });
  assert.equal(plan.cli.assetName, `${cliName}_darwin_amd64`);
  assert.equal(plan.cli.destination, path.join(rootDir, "bin", `${cliName}-bin`));
  assert.equal(plan.helper.assetName, helperAssetName);
  assert.equal(plan.helper.destination, path.join(rootDir, "bin", helperAssetName));
  assert.equal(plan.repo.owner, "amxv");
  assert.equal(plan.repo.name, "procoder");
});

test("buildInstallPlan adds Windows suffix only to the host CLI binary", () => {
  const rootDir = "/tmp/procoder";
  const plan = buildInstallPlan({
    pkg,
    cliName,
    platform: "win32",
    arch: "x64",
    rootDir
  });

  assert.equal(plan.cli.assetName, `${cliName}_windows_amd64.exe`);
  assert.equal(plan.cli.destination, path.join(rootDir, "bin", `${cliName}.exe`));
  assert.equal(plan.helper.assetName, helperAssetName);
  assert.equal(plan.helper.destination, path.join(rootDir, "bin", helperAssetName));
});

test("buildFallbackBuilds cross-compiles the linux helper from cmd/procoder-return", () => {
  const rootDir = "/tmp/procoder";
  const plan = buildInstallPlan({
    pkg,
    cliName,
    platform: "linux",
    arch: "arm64",
    rootDir
  });
  const builds = buildFallbackBuilds(plan);

  assert.equal(builds.length, 2);
  assert.equal(builds[0].args.at(-1), `./cmd/${cliName}`);
  assert.equal(builds[1].args.at(-1), "./cmd/procoder-return");
  assert.equal(builds[1].destination, path.join(rootDir, "bin", helperAssetName));
  assert.equal(builds[1].env.CGO_ENABLED, "0");
  assert.equal(builds[1].env.GOOS, "linux");
  assert.equal(builds[1].env.GOARCH, "amd64");
});

test("resolveHostTarget returns null for unsupported host targets", () => {
  assert.equal(resolveHostTarget("linux", "ia32"), null);
  assert.equal(resolveHostTarget("sunos", "x64"), null);
});

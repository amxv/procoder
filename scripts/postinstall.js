#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");
const { spawnSync } = require("node:child_process");

const pkg = require("../package.json");
const cliName = pkg.config?.cliBinaryName || "procoder";
const helperBinaryName = "procoder-return";
const helperTarget = Object.freeze({
  goos: "linux",
  goarch: "amd64"
});
const helperAssetName = `${helperBinaryName}_${helperTarget.goos}_${helperTarget.goarch}`;

const goosMap = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows"
};

const goarchMap = {
  x64: "amd64",
  arm64: "arm64"
};

function parseGitHubRepo(repositoryURL) {
  const match = (repositoryURL || "").match(/github\.com[:/](.+?)\/(.+?)(?:\.git)?$/);
  if (!match) {
    throw new Error("package.json repository.url must point to a GitHub repo.");
  }

  return {
    owner: match[1],
    name: match[2]
  };
}

function resolveHostTarget(platform, arch) {
  const goos = goosMap[platform];
  const goarch = goarchMap[arch];
  if (!goos || !goarch) {
    return null;
  }

  return { goos, goarch };
}

function installedCliBinaryName(platform, binaryName) {
  return platform === "win32" ? `${binaryName}.exe` : `${binaryName}-bin`;
}

function buildReleaseAssetName(binaryName, goos, goarch) {
  const suffix = goos === "windows" ? ".exe" : "";
  return `${binaryName}_${goos}_${goarch}${suffix}`;
}

function buildReleaseAssetURL({ repoOwner, repoName, version, assetName }) {
  return `https://github.com/${repoOwner}/${repoName}/releases/download/v${version}/${assetName}`;
}

function buildInstallPlan({
  pkg,
  cliName,
  platform = process.platform,
  arch = process.arch,
  rootDir = path.resolve(__dirname, "..")
}) {
  const repo = parseGitHubRepo(pkg.repository?.url || "");
  const hostTarget = resolveHostTarget(platform, arch);
  const binDir = path.join(rootDir, "bin");

  return {
    rootDir,
    binDir,
    platform,
    arch,
    version: pkg.version,
    repo,
    hostTarget,
    cli: {
      name: cliName,
      assetName: hostTarget ? buildReleaseAssetName(cliName, hostTarget.goos, hostTarget.goarch) : "",
      destination: path.join(binDir, installedCliBinaryName(platform, cliName))
    },
    helper: {
      name: helperBinaryName,
      assetName: helperAssetName,
      destination: path.join(binDir, helperAssetName)
    }
  };
}

async function main() {
  const plan = buildInstallPlan({ pkg, cliName });
  fs.mkdirSync(plan.binDir, { recursive: true });

  if (!plan.hostTarget) {
    console.warn(`Unsupported platform/arch for release asset download: ${plan.platform}/${plan.arch}`);
    fallbackBuildOrExit(plan);
    return;
  }

  try {
    const installed = await installReleaseAssets(plan);
    console.log(`Installed ${cliName} from ${installed.cliURL}`);
    console.log(`Installed ${helperBinaryName} from ${installed.helperURL}`);
  } catch (err) {
    console.warn(`Failed to download prebuilt binaries: ${err.message}`);
    fallbackBuildOrExit(plan);
  }
}

async function installReleaseAssets(plan) {
  const cliURL = buildReleaseAssetURL({
    repoOwner: plan.repo.owner,
    repoName: plan.repo.name,
    version: plan.version,
    assetName: plan.cli.assetName
  });
  await downloadToFile(cliURL, plan.cli.destination);
  chmodIfNeeded(plan.cli.destination, plan.platform);

  const helperURL = buildReleaseAssetURL({
    repoOwner: plan.repo.owner,
    repoName: plan.repo.name,
    version: plan.version,
    assetName: plan.helper.assetName
  });
  await downloadToFile(helperURL, plan.helper.destination);
  chmodIfNeeded(plan.helper.destination, plan.platform);

  return { cliURL, helperURL };
}

function fallbackBuildOrExit(plan) {
  const hasGo = spawnSync("go", ["version"], { stdio: "ignore" }).status === 0;
  const hasSource = [
    path.join(plan.rootDir, "cmd", plan.cli.name, "main.go"),
    path.join(plan.rootDir, "cmd", plan.helper.name, "main.go")
  ].every((sourcePath) => fs.existsSync(sourcePath));

  if (!hasGo || !hasSource) {
    console.error("Unable to install procoder binaries. Missing release assets and local Go build fallback.");
    process.exit(1);
  }

  for (const build of buildFallbackBuilds(plan)) {
    const result = spawnSync("go", build.args, {
      cwd: plan.rootDir,
      stdio: "inherit",
      env: {
        ...process.env,
        ...build.env
      }
    });

    if (result.status !== 0) {
      process.exit(result.status || 1);
    }

    chmodIfNeeded(build.destination, plan.platform);
  }

  console.log(`Installed ${plan.cli.name} and ${plan.helper.name} by building from source.`);
}

function buildFallbackBuilds(plan) {
  return [
    {
      destination: plan.cli.destination,
      env: {},
      args: [
        "build",
        "-trimpath",
        '-ldflags=-s -w',
        "-o",
        plan.cli.destination,
        `./cmd/${plan.cli.name}`
      ]
    },
    {
      destination: plan.helper.destination,
      env: {
        CGO_ENABLED: "0",
        GOOS: helperTarget.goos,
        GOARCH: helperTarget.goarch
      },
      args: [
        "build",
        "-trimpath",
        '-ldflags=-s -w',
        "-o",
        plan.helper.destination,
        `./cmd/${plan.helper.name}`
      ]
    }
  ];
}

function chmodIfNeeded(targetPath, platform) {
  if (platform !== "win32") {
    fs.chmodSync(targetPath, 0o755);
  }
}

function downloadToFile(url, destinationPath) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        downloadToFile(response.headers.location, destinationPath).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`HTTP ${response.statusCode}`));
        return;
      }

      const file = fs.createWriteStream(destinationPath);
      response.pipe(file);

      file.on("finish", () => {
        file.close((err) => {
          if (err) {
            reject(err);
            return;
          }
          resolve();
        });
      });

      file.on("error", (err) => {
        fs.rmSync(destinationPath, { force: true });
        reject(err);
      });
    });

    request.on("error", reject);
  });
}

module.exports = {
  buildFallbackBuilds,
  buildInstallPlan,
  buildReleaseAssetName,
  buildReleaseAssetURL,
  helperAssetName,
  helperBinaryName,
  installedCliBinaryName,
  parseGitHubRepo,
  resolveHostTarget
};

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}

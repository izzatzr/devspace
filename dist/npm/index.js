#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const exec = require("child_process").exec;
const request = require("request");
const Spinner = require("cli-spinner").Spinner;

const downloadPathTemplate =
  "https://github.com/devspace-cloud/devspace/releases/download/v{{version}}/devspace-{{platform}}-{{arch}}";
const ARCH_MAPPING = {
  ia32: "386",
  x64: "amd64",
  arm: "arm"
};
const PLATFORM_MAPPING = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
  freebsd: "freebsd"
};

if (
  !(process.platform in PLATFORM_MAPPING) ||
  !(process.arch in ARCH_MAPPING)
) {
  console.error(
    "Installation is not supported for this platform (" +
      process.platform +
      ") or architecture (" +
      process.arch +
      ")"
  );
  return;
}
let action;

if (process.argv && process.argv.length > 2) {
  action = process.argv[2];
} else {
  console.error("Please specify a version to publish!");
  return;
}

if (action == "noop") {
  console.log("Successfully ran noop command");
  process.exit(0);
}

const packageJsonPath = path.join(__dirname, "package.json");
if (!fs.existsSync(packageJsonPath)) {
  console.error("Unable to find package.json");
  return;
}

const requestHeaders = {
  "User-Agent": "devspace-npm-script"
};
let packageJson = JSON.parse(fs.readFileSync(packageJsonPath));

const getLatestVersion = function(callback) {
  const releasesURL = "https://github.com/devspace-cloud/devspace/releases";

  request({ uri: releasesURL, headers: requestHeaders }, function(
    err,
    res,
    releasePage
  ) {
    if (res.statusCode != 200) {
      console.error(
        "Error requesting URL " +
          releasesURL +
          " (Status Code: " +
          res.statusCode +
          ")"
      );
      console.error(err);
      process.exit(1);
    }
    const latestVersion = releasePage.replace(
      /^.*?\/devspace-cloud\/devspace\/releases\/download\/v([^\/]*)\/devspace-.*$/s,
      "$1"
    );

    if (releasePage != latestVersion && latestVersion) {
      callback(latestVersion);
    } else {
      console.error("Unable to identify latest devspace version");
      process.exit(1);
    }
  });
};

if (action == "update-version") {
  getLatestVersion(function(latestVersion) {
    packageJson.version = latestVersion;

    fs.writeFileSync(packageJsonPath, JSON.stringify(packageJson, null, 4));

    process.exit(0);
  });
  return;
}

let version = packageJson.version;
let platform = PLATFORM_MAPPING[process.platform];
let arch = ARCH_MAPPING[process.arch];
let binaryName = "devspace";

if (platform == "windows") {
  binaryName += ".exe";
}

exec("npm bin", function(err, stdout, stderr) {
  let dir = null;
  if (err || stderr || !stdout || stdout.length === 0) {
    let env = process.env;
    if (env && env.npm_config_prefix) {
      dir = path.join(env.npm_config_prefix, "bin");
    }
  } else {
    dir = stdout.trim();
  }

  if (dir == null) callback("Error finding binary installation directory");

  let binaryPath = path.join(dir, binaryName);

  if (process.argv.length > 3) {
    binaryPath = process.argv[3];
  }

  if (platform != "windows" && action == "install") {
    process.exit(0);
  }

  try {
    fs.unlinkSync(binaryPath);
  } catch (e) {}

  if (platform == "windows") {
    try {
      fs.unlinkSync(binaryPath.replace(/\.exe$/i, ""));
    } catch (e) {}

    try {
      fs.unlinkSync(binaryPath.replace(/\.exe$/i, ".cmd"));
    } catch (e) {}
  }

  if (action == "install" || action == "force-install") {
    const showRootError = function() {
      console.error("\n############################################");
      console.error(
        "Failed to download DevSpace CLI due to permission issues!\n"
      );
      console.error("There are two options to fix this:");
      console.error("1. Do not run 'npm install' as root (recommended)");
      console.error(
        "2. Run this command: npm install --unsafe-perm=true -g devspace"
      );
      console.error("   You may need to run this command using sudo.");
      console.error("############################################\n");
      process.exit(1);
    };

    const downloadRelease = function(version) {
      let downloadPath = downloadPathTemplate
        .replace("{{version}}", version)
        .replace("{{platform}}", platform)
        .replace("{{arch}}", arch);

      if (platform == "windows") {
        downloadPath += ".exe";
      }

      console.log("Download DevSpace CLI release: " + downloadPath + "\n");

      const spinner = new Spinner(
        "%s Downloading DevSpace CLI... (this may take a minute)"
      );
      spinner.setSpinnerString("|/-\\");
      spinner.start();

      let writeStream = fs
        .createWriteStream(binaryPath)
        .on("error", function(err) {
          spinner.stop(true);
          showRootError();
        });

      request({ uri: downloadPath, headers: requestHeaders, encoding: null })
        .on("error", function() {
          spinner.stop(true);
          console.error("Error requesting URL: " + downloadPath);
          process.exit(1);
        })
        .on("response", function(res) {
          spinner.stop(true);
          if (res.statusCode != 200) {
            if (res.statusCode == 404) {
              console.error("Release version " + version + " not found.\n");

              getLatestVersion(function(latestVersion) {
                if (latestVersion != version) {
                  console.log(
                    "Downloading latest stable release instead. Latest version is: " +
                      latestVersion +
                      "\n"
                  );

                  downloadRelease(latestVersion);
                }
              });
              return;
            } else {
              console.error(
                "Error requesting URL " +
                  downloadPath +
                  " (Status Code: " +
                  res.statusCode +
                  ")"
              );
              console.error(err);
              process.exit(1);
            }
          } else {
            try {
              res.pipe(writeStream);
            } catch (e) {
              showRootError();
            }
          }
        })
        .on("end", function() {
          writeStream.end();
          spinner.stop(true);

          try {
            fs.chmodSync(binaryPath, 0755);
          } catch (e) {
            showRootError();
          }
        });
    };

    downloadRelease(version);
  }
});

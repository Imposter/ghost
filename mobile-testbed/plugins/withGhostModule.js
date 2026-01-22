/**
 * Expo config plugin for Ghost native module integration.
 *
 * This plugin:
 * 1. Copies ghost.aar from ghost-go/build/ to android/app/libs/
 * 2. Adds the libs directory to build.gradle dependencies
 * 3. Registers GhostPackage in MainApplication
 */

const { withAppBuildGradle, withMainApplication, withDangerousMod } = require('@expo/config-plugins');
const fs = require('fs');
const path = require('path');

function withGhostAarCopy(config) {
  return withDangerousMod(config, [
    'android',
    async (config) => {
      const projectRoot = config.modRequest.projectRoot;
      const libsDir = path.join(projectRoot, 'android', 'app', 'libs');
      const sourceAar = path.join(projectRoot, '..', 'ghost-go', 'build', 'ghost.aar');
      const destAar = path.join(libsDir, 'ghost.aar');

      // Create libs directory if it doesn't exist
      if (!fs.existsSync(libsDir)) {
        fs.mkdirSync(libsDir, { recursive: true });
        console.log('[withGhostModule] Created libs directory');
      }

      // Copy ghost.aar if source exists
      if (fs.existsSync(sourceAar)) {
        fs.copyFileSync(sourceAar, destAar);
        console.log('[withGhostModule] Copied ghost.aar to android/app/libs/');
      } else {
        console.warn('[withGhostModule] WARNING: ghost.aar not found at', sourceAar);
        console.warn('[withGhostModule] Run "make android" in ghost-go/ to build it');
      }

      return config;
    },
  ]);
}

function withGhostBuildGradle(config) {
  return withAppBuildGradle(config, (config) => {
    let buildGradle = config.modResults.contents;

    // Add libs directory to repositories if not already present
    if (!buildGradle.includes('flatDir')) {
      // Find the android { block and add repositories
      const androidBlockMatch = buildGradle.match(/android\s*\{/);
      if (androidBlockMatch) {
        const insertPos = androidBlockMatch.index + androidBlockMatch[0].length;
        const repoBlock = `
    // Ghost native module - include local AAR files
    repositories {
        flatDir {
            dirs 'libs'
        }
    }
`;
        buildGradle = buildGradle.slice(0, insertPos) + repoBlock + buildGradle.slice(insertPos);
        console.log('[withGhostModule] Added flatDir repository to build.gradle');
      }
    }

    // Add the ghost.aar dependency if not already present
    if (!buildGradle.includes("ghost@aar")) {
      // Find the dependencies block
      const depsMatch = buildGradle.match(/dependencies\s*\{/);
      if (depsMatch) {
        const insertPos = depsMatch.index + depsMatch[0].length;
        const depLine = `
    implementation(name: 'ghost', ext: 'aar')
`;
        buildGradle = buildGradle.slice(0, insertPos) + depLine + buildGradle.slice(insertPos);
        console.log('[withGhostModule] Added ghost.aar dependency to build.gradle');
      }
    }

    config.modResults.contents = buildGradle;
    return config;
  });
}

function withGhostMainApplication(config) {
  return withMainApplication(config, (config) => {
    let mainApp = config.modResults.contents;

    // Add import for GhostPackage if not present
    if (!mainApp.includes('com.ghost.testbed.GhostPackage')) {
      // Find the import section (after package declaration)
      const importMatch = mainApp.match(/import\s+/);
      if (importMatch) {
        const insertPos = importMatch.index;
        mainApp = mainApp.slice(0, insertPos) + 'import com.ghost.testbed.GhostPackage\n' + mainApp.slice(insertPos);
        console.log('[withGhostModule] Added GhostPackage import');
      }
    }

    // Add GhostPackage to the packages list
    // Look for the getPackages() method and add our package
    if (!mainApp.includes('GhostPackage()')) {
      // For Kotlin MainApplication, find the packages list
      const packagesMatch = mainApp.match(/override\s+fun\s+getPackages\s*\(\s*\)\s*:\s*List<ReactPackage>\s*\{[^}]*packages\.apply\s*\{/s);
      if (packagesMatch) {
        const applyEnd = mainApp.indexOf('}', packagesMatch.index + packagesMatch[0].length);
        if (applyEnd !== -1) {
          mainApp = mainApp.slice(0, applyEnd) + '\n                add(GhostPackage())\n            ' + mainApp.slice(applyEnd);
          console.log('[withGhostModule] Added GhostPackage to packages list');
        }
      } else {
        // Try alternative pattern for simpler MainApplication
        const packagesListMatch = mainApp.match(/PackageList\s*\(\s*this\s*\)\s*\.packages/);
        if (packagesListMatch) {
          // Find where to add the package
          const afterPackageList = mainApp.indexOf('.packages', packagesListMatch.index) + '.packages'.length;
          // Insert .apply { add(GhostPackage()) } after packages
          const existingApply = mainApp.slice(afterPackageList, afterPackageList + 20).includes('.apply');
          if (!existingApply) {
            mainApp = mainApp.slice(0, afterPackageList) + '.apply { add(GhostPackage()) }' + mainApp.slice(afterPackageList);
            console.log('[withGhostModule] Added GhostPackage via apply block');
          }
        }
      }
    }

    config.modResults.contents = mainApp;
    return config;
  });
}

module.exports = function withGhostModule(config) {
  config = withGhostAarCopy(config);
  config = withGhostBuildGradle(config);
  config = withGhostMainApplication(config);
  return config;
};

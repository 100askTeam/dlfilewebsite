#!/usr/bin/env node

import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const productPattern = /^[a-z][a-z0-9_-]{1,63}$/;
const targetPattern = /^[a-z][a-z0-9_-]{1,63}$/;
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z][0-9A-Za-z.-]{0,63})?$/;
const repositoryPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}

function signatureFiles(source) {
  const result = new Map();
  for (const name of readdirSync(source)) {
    if (!name.endsWith('.sig')) continue;
    const file = name.slice(0, -4);
    const filePath = join(source, file);
    const signaturePath = join(source, name);
    if (!existsSync(filePath) || !statSync(filePath).isFile() || !statSync(signaturePath).isFile()) {
      continue;
    }
    const signature = readFileSync(signaturePath, 'utf8').trim();
    const matches = result.get(signature) || [];
    matches.push(file);
    result.set(signature, matches);
  }
  return result;
}

function releaseFileFromURL(value, repository, tag, signatures, signature) {
  const url = new URL(value);
  requireValue(url.protocol === 'https:', `Updater URL must use HTTPS: ${value}`);
  requireValue(url.username === '' && url.password === '' && url.hash === '', `Unsafe updater URL: ${value}`);
  const prefix = `/${repository}/releases/download/${encodeURIComponent(tag)}/`;
  let name;
  if (url.hostname === 'github.com' && url.pathname.startsWith(prefix)) {
    const encodedName = url.pathname.slice(prefix.length);
    requireValue(encodedName && !encodedName.includes('/'), `Updater URL has an invalid filename: ${value}`);
    name = decodeURIComponent(encodedName);
  } else {
    const apiPrefix = `/repos/${repository}/releases/assets/`;
    const assetId = url.pathname.slice(apiPrefix.length);
    requireValue(
      url.hostname === 'api.github.com' &&
        url.pathname.startsWith(apiPrefix) &&
        /^[1-9]\d*$/.test(assetId),
      `Updater URL is outside ${repository} ${tag}: ${value}`
    );
    const matches = signatures.get(signature.trim()) || [];
    requireValue(
      matches.length === 1,
      `GitHub asset URL cannot be mapped to exactly one signed local file: ${value}`
    );
    [name] = matches;
  }
  requireValue(basename(name) === name && name !== '.' && name !== '..', `Unsafe updater filename: ${name}`);
  return name;
}

export function prepareTauriRelease({ sourceDirectory, outputDirectory, product, channel, repository }) {
  requireValue(productPattern.test(product), `Invalid product slug: ${product}`);
  requireValue(['stable', 'beta', 'nightly'].includes(channel), `Invalid release channel: ${channel}`);
  requireValue(repositoryPattern.test(repository), `Invalid release repository: ${repository}`);

  const source = resolve(sourceDirectory);
  const output = resolve(outputDirectory);
  requireValue(source !== output, 'Source and output release directories must differ.');
  requireValue(existsSync(source) && statSync(source).isDirectory(), `Release source directory is absent: ${source}`);
  requireValue(!existsSync(output), `Refusing to replace existing output directory: ${output}`);

  const latestPath = join(source, 'latest.json');
  requireValue(existsSync(latestPath), `latest.json is missing from ${source}`);
  const latest = JSON.parse(readFileSync(latestPath, 'utf8'));
  requireValue(versionPattern.test(latest.version || ''), `Invalid updater version: ${latest.version}`);
  requireValue(channel !== 'stable' || !latest.version.includes('-'), 'Stable releases cannot contain a prerelease version.');
  requireValue(Number.isFinite(Date.parse(latest.pub_date)), 'latest.json pub_date must be RFC3339.');
  requireValue(latest.platforms && typeof latest.platforms === 'object' && !Array.isArray(latest.platforms), 'latest.json platforms are missing.');

  const tag = `v${latest.version}`;
  const signatures = signatureFiles(source);
  const assets = [];
  const sourceFiles = new Map();
  for (const [target, entry] of Object.entries(latest.platforms)) {
    requireValue(targetPattern.test(target), `Invalid updater target: ${target}`);
    requireValue(entry && typeof entry === 'object', `Updater target ${target} is invalid.`);
    requireValue(typeof entry.signature === 'string' && entry.signature.trim().length > 20, `Updater signature is missing for ${target}.`);
    const file = releaseFileFromURL(entry.url, repository, tag, signatures, entry.signature);
    const path = join(source, file);
    requireValue(existsSync(path) && statSync(path).isFile(), `Updater asset is missing: ${file}`);
    const data = readFileSync(path);
    requireValue(data.length > 0, `Updater asset is empty: ${file}`);
    const sha256 = createHash('sha256').update(data).digest('hex');
    assets.push({
      target,
      kind: 'full',
      file,
      size: data.length,
      sha256,
      signature: entry.signature.trim(),
      mirrors: [
        `https://github.com/${repository}/releases/download/${encodeURIComponent(tag)}/${encodeURIComponent(file)}`,
      ],
    });
    sourceFiles.set(file, path);
  }
  requireValue(assets.length > 0 && assets.length <= 128, 'Updater asset route count is invalid.');

  assets.sort((left, right) => left.target.localeCompare(right.target));
  mkdirSync(output, { recursive: false });
  for (const [file, path] of [...sourceFiles.entries()].sort(([left], [right]) => left.localeCompare(right))) {
    copyFileSync(path, join(output, file), 0);
  }
  const manifest = {
    schema_version: 1,
    product,
    channel,
    version: latest.version,
    published_at: new Date(latest.pub_date).toISOString(),
    notes: typeof latest.notes === 'string' ? latest.notes : '',
    assets,
  };
  writeFileSync(join(output, 'release-set.json'), `${JSON.stringify(manifest, null, 2)}\n`, { flag: 'wx', mode: 0o640 });
  return manifest;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [sourceDirectory, outputDirectory, product, channel, repository] = process.argv.slice(2);
  requireValue(sourceDirectory && outputDirectory && product && channel && repository, 'Usage: prepare-tauri-release.mjs SOURCE_DIR OUTPUT_DIR PRODUCT CHANNEL OWNER/REPO');
  const manifest = prepareTauriRelease({ sourceDirectory, outputDirectory, product, channel, repository });
  process.stdout.write(`Prepared ${manifest.product}/${manifest.channel}/${manifest.version}: ${manifest.assets.length} routes.\n`);
}

import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { prepareTauriRelease } from './prepare-tauri-release.mjs';

test('prepares a deduplicated product-neutral Tauri release set', () => {
  const root = mkdtempSync(join(tmpdir(), 'dl-tauri-release-'));
  try {
    const source = join(root, 'source');
    const output = join(root, 'output');
    mkdirSync(source);
    const file = 'USBToolBox_1.0.10_x64-setup.exe';
    writeFileSync(join(source, file), 'signed updater bytes');
    writeFileSync(
      join(source, 'latest.json'),
      JSON.stringify({
        version: '1.0.10',
        notes: 'USBToolBox update',
        pub_date: '2026-09-15T12:00:00Z',
        platforms: {
          'windows-x86_64': {
            url: `https://github.com/dshanpi/DshanPI_USBToolBox/releases/download/v1.0.10/${file}`,
            signature: 'base64-minisign-signature-placeholder',
          },
          'windows-x86_64-nsis': {
            url: `https://github.com/dshanpi/DshanPI_USBToolBox/releases/download/v1.0.10/${file}`,
            signature: 'base64-minisign-signature-placeholder',
          },
        },
      })
    );
    const manifest = prepareTauriRelease({
      sourceDirectory: source,
      outputDirectory: output,
      product: 'usbtoolbox',
      channel: 'stable',
      repository: 'dshanpi/DshanPI_USBToolBox',
    });
    assert.equal(manifest.assets.length, 2);
    assert.equal(manifest.assets[0].file, file);
    assert.equal(readFileSync(join(output, file), 'utf8'), 'signed updater bytes');
    assert.deepEqual(
      JSON.parse(readFileSync(join(output, 'release-set.json'), 'utf8')),
      manifest
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('rejects cross-repository updater URLs and existing output', () => {
  const root = mkdtempSync(join(tmpdir(), 'dl-tauri-release-reject-'));
  try {
    const source = join(root, 'source');
    mkdirSync(source);
    writeFileSync(join(source, 'asset.exe'), 'bytes');
    writeFileSync(
      join(source, 'latest.json'),
      JSON.stringify({
        version: '1.0.10',
        pub_date: '2026-09-15T12:00:00Z',
        platforms: {
          'windows-x86_64': {
            url: 'https://github.com/attacker/repo/releases/download/v1.0.10/asset.exe',
            signature: 'base64-minisign-signature-placeholder',
          },
        },
      })
    );
    assert.throws(
      () =>
        prepareTauriRelease({
          sourceDirectory: source,
          outputDirectory: join(root, 'output'),
          product: 'usbtoolbox',
          channel: 'stable',
          repository: 'dshanpi/DshanPI_USBToolBox',
        }),
      /outside/
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('maps tauri-action API asset URLs by exact local signature', () => {
  const root = mkdtempSync(join(tmpdir(), 'dl-tauri-api-release-'));
  try {
    const source = join(root, 'source');
    const output = join(root, 'output');
    mkdirSync(source);
    const file = 'USBToolBox_1.0.10_x64-setup.exe';
    const signature = 'base64-minisign-signature-for-api-route';
    writeFileSync(join(source, file), 'signed updater bytes');
    writeFileSync(join(source, `${file}.sig`), signature);
    writeFileSync(
      join(source, 'latest.json'),
      JSON.stringify({
        version: '1.0.10',
        notes: 'USBToolBox update',
        pub_date: '2026-09-15T12:00:00Z',
        platforms: {
          'windows-x86_64-nsis': {
            url: 'https://api.github.com/repos/dshanpi/DshanPI_USBToolBox/releases/assets/123456',
            signature,
          },
        },
      })
    );
    const manifest = prepareTauriRelease({
      sourceDirectory: source,
      outputDirectory: output,
      product: 'usbtoolbox',
      channel: 'stable',
      repository: 'dshanpi/DshanPI_USBToolBox',
    });
    assert.equal(manifest.assets[0].file, file);
    assert.equal(
      manifest.assets[0].mirrors[0],
      `https://github.com/dshanpi/DshanPI_USBToolBox/releases/download/v1.0.10/${file}`
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

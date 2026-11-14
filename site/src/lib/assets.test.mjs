import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { findAsset, mediaSrc, mediaKind, isRoutable } from './assets.mjs';
import { features } from '../data/features.mjs';

const ASSETS = [
  { id: 'dashboard-main', kind: 'image', file: 'src/assets/dashboard.png', version: '0.4.12' },
  { id: 'ship-run', kind: 'video', file: '/media/ship-run.webm', version: '0.4.12' },
];

test('findAsset returns the entry of a known id', () => {
  assert.equal(findAsset(ASSETS, 'ship-run'), ASSETS[1]);
});

test('findAsset throws for an absent id', () => {
  assert.throws(() => findAsset(ASSETS, 'no-such-asset'), { message: 'unknown asset id no-such-asset' });
});

test('mediaSrc keeps a path that already has the base prefix', () => {
  assert.equal(mediaSrc('/sdlc-plugin/media/x.webm', '/sdlc-plugin'), '/sdlc-plugin/media/x.webm');
});

test('mediaSrc adds the base prefix to a path without it', () => {
  assert.equal(mediaSrc('/media/x.webm', '/sdlc-plugin'), '/sdlc-plugin/media/x.webm');
});

test('mediaKind returns image for an image entry', () => {
  assert.equal(mediaKind({ kind: 'image' }), 'image');
});

test('mediaKind returns video for a video entry', () => {
  assert.equal(mediaKind({ kind: 'video' }), 'video');
});

test('mediaKind throws for an unknown kind', () => {
  assert.throws(() => mediaKind({ kind: 'audio' }), { message: 'unknown media kind audio' });
});

test('isRoutable is true for an entry with claims', () => {
  assert.equal(isRoutable({ claims: [{ text: 'x', doc: 'docs/dashboard.md' }] }), true);
});

test('isRoutable is false for an entry with no claims', () => {
  assert.equal(isRoutable({ claims: [] }), false);
});

test('assets.json has the dashboard-main image with its own version', () => {
  const assets = JSON.parse(fs.readFileSync(new URL('../data/assets.json', import.meta.url), 'utf8'));
  const asset = findAsset(assets, 'dashboard-main');
  assert.equal(mediaKind(asset), 'image');
  assert.match(asset.version, /^\d+\.\d+\.\d+$/);
  assert.ok(fs.existsSync(new URL(`../../${asset.file}`, import.meta.url)), `${asset.file} exists`);
});

test('features lists the 5 final slugs, each with a summary and a description', () => {
  assert.deepEqual(features.map((f) => f.slug), ['customize', 'dashboard', 'stats', 'harden', 'long-pipelines']);
  for (const feature of features) {
    assert.ok(feature.summary && feature.summary !== '…', `${feature.slug} summary`);
    assert.ok(feature.description && feature.description !== '…', `${feature.slug} description`);
    assert.ok(Array.isArray(feature.media) && Array.isArray(feature.claims), `${feature.slug} media and claims`);
  }
});

// Lookups for the media of the site: the entries of src/data/assets.json and
// the entries of src/data/features.mjs. The branches live here, not in the
// components, so that node --test covers them.

/**
 * Returns the entry of `assets` with the id `id`.
 * An id that is not in the list stops the build.
 */
export function findAsset(assets, id) {
  const asset = assets.find((entry) => entry.id === id);
  if (!asset) {
    throw new Error(`unknown asset id ${id}`);
  }
  return asset;
}

/**
 * Prefixes a root-relative media path with the site base.
 * '/media/x.webm' with base '/sdlc-plugin' -> '/sdlc-plugin/media/x.webm'.
 * A path that already starts with the base stays as it is.
 */
export function mediaSrc(path, base) {
  const prefix = base.endsWith('/') ? base.slice(0, -1) : base;
  if (prefix === '' || path === prefix || path.startsWith(`${prefix}/`)) {
    return path;
  }
  return `${prefix}${path.startsWith('/') ? '' : '/'}${path}`;
}

/** Returns 'image' or 'video'. Any other kind stops the build. */
export function mediaKind(asset) {
  if (asset.kind === 'image' || asset.kind === 'video') {
    return asset.kind;
  }
  throw new Error(`unknown media kind ${asset.kind}`);
}

/** A feature gets its own product page only when it has 1 or more claims. */
export function isRoutable(feature) {
  return Array.isArray(feature.claims) && feature.claims.length > 0;
}

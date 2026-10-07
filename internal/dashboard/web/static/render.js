/**
 * DOM builders for the sdlc dashboard page. Each builder takes the document
 * as its first argument, so the same file runs as a browser script
 * (window.sdlcRender) and under Node's test runner (module.exports) with a
 * fake document. Snapshot text goes into the page through textContent only.
 */
(function (root) {
  'use strict';

  var api = {};

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  } else {
    root.sdlcRender = api;
  }
})(this);

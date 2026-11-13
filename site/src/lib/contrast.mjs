// WCAG 2.2 contrast check. Text pairs need 4.5 (1.4.3). Control and part edges need 3 (1.4.11).

/**
 * Parse a hex or rgba() color into 0-255 channels and an alpha of 0-1.
 * @param {string} color
 * @returns {{ r: number, g: number, b: number, a: number }}
 */
function parseColor(color) {
  const value = String(color).trim().toLowerCase();

  const hex = value.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/);
  if (hex) {
    const digits = hex[1].length === 3 ? [...hex[1]].map((d) => d + d).join('') : hex[1];
    return {
      r: parseInt(digits.slice(0, 2), 16),
      g: parseInt(digits.slice(2, 4), 16),
      b: parseInt(digits.slice(4, 6), 16),
      a: 1,
    };
  }

  const rgba = value.match(/^rgba?\(\s*([^)]+)\)$/);
  if (rgba) {
    const parts = rgba[1].split(/[\s,/]+/).filter(Boolean);
    if (parts.length === 3 || parts.length === 4) {
      const channels = parts.slice(0, 3).map(Number);
      let alpha = 1;
      if (parts.length === 4) {
        alpha = parts[3].endsWith('%') ? Number(parts[3].slice(0, -1)) / 100 : Number(parts[3]);
      }
      const valid =
        channels.every((c) => Number.isFinite(c) && c >= 0 && c <= 255) &&
        Number.isFinite(alpha) &&
        alpha >= 0 &&
        alpha <= 1;
      if (valid) return { r: channels[0], g: channels[1], b: channels[2], a: alpha };
    }
  }

  throw new Error(`Unsupported color: ${color}`);
}

/** @param {number} channel 0-255 */
function linearize(channel) {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/** @param {{ r: number, g: number, b: number }} rgb */
function luminance({ r, g, b }) {
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

/**
 * Contrast ratio of a foreground color over an opaque background.
 * An rgba() foreground is mixed over the background before the ratio.
 * @param {string} fg hex or rgba()
 * @param {string} bg opaque hex
 * @returns {number}
 */
export function contrastRatio(fg, bg) {
  const back = parseColor(bg);
  if (back.a !== 1) throw new Error(`Background must be opaque: ${bg}`);
  const front = parseColor(fg);
  const mixed = {
    r: front.r * front.a + back.r * (1 - front.a),
    g: front.g * front.a + back.g * (1 - front.a),
    b: front.b * front.a + back.b * (1 - front.a),
  };
  const l1 = luminance(mixed);
  const l2 = luminance(back);
  const [light, dark] = l1 >= l2 ? [l1, l2] : [l2, l1];
  return (light + 0.05) / (dark + 0.05);
}

/**
 * Return each pair whose ratio is below its minimum.
 * The ratio is not rounded, so 4.499 fails a minimum of 4.5.
 * @param {{ fg: string, bg: string, min: 4.5 | 3, label: string }[]} pairs
 * @returns {{ label: string, ratio: number, min: number }[]}
 */
export function checkPairs(pairs) {
  const failures = [];
  for (const { fg, bg, min, label } of pairs) {
    const ratio = contrastRatio(fg, bg);
    if (ratio < min) failures.push({ label, ratio, min });
  }
  return failures;
}

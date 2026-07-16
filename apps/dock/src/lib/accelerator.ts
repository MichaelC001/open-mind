export type CapturedKey = {
  key: string;
  code: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
};

export const DEFAULT_QUICK_SAVE = "CmdOrCtrl+Shift+S";
export const DEFAULT_QUICK_FIND = "CmdOrCtrl+Shift+O";

const MODIFIER_NAMES = new Set(["Shift", "Control", "Alt", "Meta", "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight", "AltLeft", "AltRight", "MetaLeft", "MetaRight"]);

function keyFromCode(code: string): string | null {
  if (code.startsWith("Key")) {
    return code.slice(3);
  }
  if (code.startsWith("Digit")) {
    return code.slice(5);
  }
  if (code.startsWith("F") && /^F\d+$/.test(code)) {
    return code;
  }

  const codeMap: Record<string, string> = {
    Space: "Space",
    Minus: "-",
    Equal: "=",
    Comma: ",",
    Period: ".",
    Slash: "/",
    Semicolon: ";",
    Quote: "'",
    BracketLeft: "[",
    BracketRight: "]",
    Backquote: "`",
  };

  return codeMap[code] ?? null;
}

export function captureToAccelerator(e: CapturedKey): string | null {
  if (MODIFIER_NAMES.has(e.key)) {
    return null;
  }

  const key = keyFromCode(e.code);
  if (key === null) {
    return null;
  }

  const hasModifier = e.metaKey || e.ctrlKey || e.altKey || e.shiftKey;
  if (!hasModifier) {
    return null;
  }

  const parts: string[] = [];

  if (e.metaKey || e.ctrlKey) {
    parts.push("CmdOrCtrl");
  }

  if (e.altKey) {
    parts.push("Alt");
  }

  if (e.shiftKey) {
    parts.push("Shift");
  }

  parts.push(key);

  return parts.join("+");
}

/** Tokens from the global-hotkey crate's `Shortcut: Display` form (what
 * `get_shortcuts` returns, e.g. "shift+super+KeyS") mapped to our canonical
 * "+"-joined accelerator tokens (e.g. "Shift", "CmdOrCtrl", "S") — the same
 * vocabulary `captureToAccelerator` produces and `rebind_shortcuts` parses. */
const DISPLAY_TOKEN_MAP: Record<string, string> = {
  shift: "Shift",
  alt: "Alt",
  option: "Alt",
};

/** Modifier tokens the plugin's Display form uses for the OS-primary key —
 * `super`/`meta` on macOS, but a bare `ctrl` shows up on Windows/Linux. Both
 * map to our single cross-platform "CmdOrCtrl" token. */
const PRIMARY_MODIFIER_TOKENS = new Set(["super", "meta", "ctrl", "control"]);

function normaliseDisplayKeyToken(token: string): string {
  if (token.startsWith("Key") && token.length === 4) {
    return token.slice(3);
  }
  if (token.startsWith("Digit")) {
    return token.slice(5);
  }
  if (/^F\d+$/i.test(token)) {
    return token.toUpperCase();
  }
  if (token.toLowerCase() === "space") {
    return "Space";
  }
  return token;
}

/**
 * Maps global-hotkey's Display accelerator ("shift+super+KeyS") into our
 * canonical form ("CmdOrCtrl+Shift+S") so Settings shows the same style the
 * user types when recording a new shortcut. Order is fixed (CmdOrCtrl, Alt,
 * Shift, key) to match `captureToAccelerator`'s output. Unknown tokens pass
 * through unchanged in their original position, appended after known
 * modifiers/key, so a surprising plugin token never gets silently dropped.
 */
export function normaliseDisplay(accel: string): string {
  const tokens = accel.split("+").filter(Boolean);
  if (tokens.length === 0) return accel;

  let hasPrimary = false;
  let hasAlt = false;
  let hasShift = false;
  const unknown: string[] = [];
  let keyToken: string | null = null;

  for (const raw of tokens) {
    const lower = raw.toLowerCase();
    if (PRIMARY_MODIFIER_TOKENS.has(lower)) {
      hasPrimary = true;
      continue;
    }
    const mapped = DISPLAY_TOKEN_MAP[lower];
    if (mapped === "Shift") {
      hasShift = true;
      continue;
    }
    if (mapped === "Alt") {
      hasAlt = true;
      continue;
    }
    // Last non-modifier token is treated as the key; anything else unknown
    // is preserved verbatim, in order.
    if (keyToken !== null) {
      unknown.push(keyToken);
    }
    keyToken = raw;
  }

  const parts: string[] = [];
  if (hasPrimary) parts.push("CmdOrCtrl");
  if (hasAlt) parts.push("Alt");
  if (hasShift) parts.push("Shift");
  parts.push(...unknown);
  if (keyToken !== null) parts.push(normaliseDisplayKeyToken(keyToken));

  return parts.join("+");
}

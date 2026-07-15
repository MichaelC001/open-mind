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
    Space: " ",
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

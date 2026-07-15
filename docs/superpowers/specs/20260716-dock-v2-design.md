# Dock v2: shortcut rebinding + quick-tag — design

Date: 2026-07-16. Feature 3 of the four-feature run. Scope (user-confirmed):
shortcut rebinding and quick-tag-after-save only; offline queue,
Windows/Linux, and auto-update stay backlog candidates.

## Shortcut rebinding

- Settings gains two recorder fields (Quick Save, Quick Find): focus the
  field, press a combo; captured on keydown and normalised to a Tauri
  accelerator string (`CmdOrCtrl+Shift+S` style). Validation: at least one
  modifier plus one non-modifier key; otherwise inline error. "Reset to
  defaults" link (defaults unchanged: Quick Save ⌘⇧S, Quick Find ⌘⇧O).
- Stored with the existing dock settings store (settings.ts) — not the
  keychain (not secrets).
- Apply: new Rust command `rebind_shortcuts(quick_save, quick_find)` —
  unregister old accelerators, register new ones. A registration failure
  (combo taken by the OS/another app) degrades exactly like startup does
  today (notify, tray still works), returns the failure to the frontend,
  and Settings shows "that combination is taken" inline and reverts the
  field. Startup reads the stored accelerators (fallback to defaults on
  parse failure).

## Quick-tag after save

- Rust Quick Save flow (hotkey or tray), on successful POST, emits
  `save-confirmed {itemId, title}` to the panel window and shows the panel.
- Panel renders a compact confirm strip: "Saved — <title>" + a tag input
  (comma-separated; canonicalisation stays server-side via the existing
  PATCH semantics). Enter → `PATCH /items/{itemId} {userTags}` → brief
  success tick → panel hides. Esc or 5 s idle → hide without tagging (the
  save already happened — capture is sacred, tagging is optional).
- Panel-originated saves (URL/note in Quick Find's save mode) show the same
  confirm strip inline after their save succeeds.
- Failures reuse the panel's existing error surfaces (12 s request
  deadlines, retry affordances from the reliability pass).

## Out of scope

Offline queue, Windows/Linux tab-grabbing, auto-update, per-shortcut
enable/disable, chorded shortcuts.

## Testing

- Vitest: accelerator normalisation table (letters, digits, F-keys,
  punctuation; rejects modifier-only and bare keys), tag-string parsing,
  confirm-strip state machine as a pure reducer (enter/esc/idle-timeout
  paths).
- Rust: accelerator validation unit test if re-validated Rust-side;
  `cargo build` + existing cargo tests green; `tsc` + vite build green.
- Manual e2e (user-driven; hotkeys + Automation consent can't run
  headless): rebind both shortcuts, trigger a Quick Save, tag it from the
  confirm strip, verify the tag on the web detail page. Checklist delivered
  at hand-off.

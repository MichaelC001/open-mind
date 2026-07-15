export type ConfirmState =
  | { kind: "hidden" }
  | { kind: "confirming"; itemId: string; title: string; tags: string }
  | { kind: "saving-tags"; itemId: string; title: string; tags: string }
  | { kind: "done" };

export type ConfirmEvent =
  | { type: "saved"; itemId: string; title: string }
  | { type: "type-tags"; tags: string }
  | { type: "submit" }
  | { type: "submit-ok" }
  | { type: "submit-failed" }
  | { type: "dismiss" }
  | { type: "idle-timeout" };

export function parseTags(raw: string): string[] {
  return raw
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => tag.length > 0);
}

export function confirmReduce(s: ConfirmState, e: ConfirmEvent): ConfirmState {
  switch (s.kind) {
    case "hidden": {
      if (e.type === "saved") {
        return {
          kind: "confirming",
          itemId: e.itemId,
          title: e.title,
          tags: "",
        };
      }
      return s;
    }

    case "confirming": {
      switch (e.type) {
        case "type-tags":
          return { ...s, tags: e.tags };
        case "submit":
          return { kind: "saving-tags", itemId: s.itemId, title: s.title, tags: s.tags };
        case "dismiss":
        case "idle-timeout":
          return { kind: "hidden" };
        case "saved":
          return {
            kind: "confirming",
            itemId: e.itemId,
            title: e.title,
            tags: "",
          };
        default:
          return s;
      }
    }

    case "saving-tags": {
      switch (e.type) {
        case "submit-ok":
          return { kind: "done" };
        case "submit-failed":
          return {
            kind: "confirming",
            itemId: s.itemId,
            title: s.title,
            tags: s.tags,
          };
        default:
          return s;
      }
    }

    case "done": {
      return s;
    }
  }
}

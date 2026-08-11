import { tokens } from "@openmind/ui";
import type { Item } from "../lib/types";
import { ItemCard } from "./ItemCard";
import { CardLink } from "./CardLink";

export function Grid({ items, colorActive }: { items: Item[]; colorActive?: boolean }) {
  if (items.length === 0) {
    return (
      <p
        style={{
          fontFamily: tokens.font.quote,
          fontStyle: "italic",
          fontSize: "1.25rem",
          color: tokens.color.inkMuted,
          marginTop: "2rem",
        }}
      >
        {colorActive
          ? "No saves match that colour yet — colours come from each save's palette, so try a warmer or cooler shade."
          : "Nothing gathered yet — drop a link or a thought above."}
      </p>
    );
  }

  return (
    <div className="mind-col">
      {items.map((item) => (
        <article key={item.id} className="card-wrap">
          {item.pinnedAt ? (
            <span
              aria-label="On desk"
              title="On desk"
              style={{
                position: "absolute",
                top: 10,
                right: 10,
                zIndex: 1,
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: tokens.color.gold,
                boxShadow: `0 0 0 2px ${tokens.color.cardSurface}`,
                pointerEvents: "none",
              }}
            />
          ) : null}
          <ItemCard item={item} />
          <CardLink href={`/item/${item.id}`} label={item.title ?? "Open item"} />
        </article>
      ))}
    </div>
  );
}

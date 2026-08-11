"use client";
// The stretched overlay anchor that makes a whole grid card clickable.
//
// It exists as a client component purely to warm the target on intent. Card
// links point at /item/[id], which is dynamic (apiFetch reads cookies), and for
// a dynamic route Next's default "auto" prefetch only warms the shell — so a
// card click paid the full origin round trip. prefetch={true} would fix that but
// is the wrong trade here: a grid renders up to 50 cards, and eagerly asking the
// server to render 50 item pages on every view is far more traffic than the one
// page the reader actually opens.
//
// So prefetch stays off until pointer-enter or keyboard focus, at which point
// the one card being considered is fetched in full. Flipping Link's `prefetch`
// prop is the public API for this; `true` also parks the result under
// staleTimes.static rather than the 0s dynamic default, so it survives the gap
// between hovering and clicking.
import Link from "next/link";
import { useState } from "react";

export function CardLink({ href, label }: { href: string; label: string }) {
  const [warm, setWarm] = useState(false);
  const warmUp = () => setWarm(true);

  return (
    <Link
      href={href}
      aria-label={label}
      className="card-link"
      prefetch={warm}
      onPointerEnter={warmUp}
      onFocus={warmUp}
      style={{ color: "inherit", textDecoration: "none" }}
    />
  );
}

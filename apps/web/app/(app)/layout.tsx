import type { ReactNode } from "react";
import { Shell } from "../../components/Shell";

// Every route that wears the sidebar lives in this group. The group is purely
// organisational — parentheses keep it out of the URL, so /desk, /feed, /lens/x
// and the rest are unchanged.
//
// Shell used to be rendered by each page instead. That meant the sidebar was
// part of every page's RSC payload and was re-rendered (and its /lenses +
// /account calls re-issued) on every single navigation. As a layout it is
// rendered once and stays mounted, so a navigation only swaps the main column —
// which is also what lets loading.tsx show a skeleton in the content area
// without blanking the chrome around it.
export default function AppLayout({ children }: { children: ReactNode }) {
  return <Shell>{children}</Shell>;
}

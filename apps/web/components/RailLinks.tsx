"use client";

import { useState } from "react";
import { LinkedSection } from "./LinkedSection";
import { RelatedRail } from "./RelatedRail";

/**
 * Client parent for the rail's Links + Related sections, sharing a refresh
 * token so a one-tap link from the Related rail refetches the Linked list.
 */
export function RailLinks({ itemId }: { itemId: string }) {
  const [linksVersion, setLinksVersion] = useState(0);
  return (
    <>
      <LinkedSection itemId={itemId} version={linksVersion} />
      <RelatedRail itemId={itemId} onLinked={() => setLinksVersion((v) => v + 1)} />
    </>
  );
}

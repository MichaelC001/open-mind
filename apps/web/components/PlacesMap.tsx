"use client";
// Full-bleed MapLibre map of every geocoded place. Client-side only — OSM
// raster tiles, no server component. Pins open a popup linking to the item.
import { useEffect, useRef } from "react";
import maplibregl from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";
import { tokens } from "@openmind/ui";

export type MapPlace = {
  id: string;
  name: string;
  address: string;
  lat?: number;
  lng?: number;
  itemId: string;
  itemTitle: string;
};

const OSM_STYLE = {
  version: 8 as const,
  sources: {
    osm: {
      type: "raster" as const,
      tiles: ["https://tile.openstreetmap.org/{z}/{x}/{y}.png"],
      tileSize: 256,
      attribution: "© OpenStreetMap contributors",
    },
  },
  layers: [{ id: "osm", type: "raster" as const, source: "osm" }],
};

export function PlacesMap({ places }: { places: MapPlace[] }) {
  const container = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!container.current) return;
    const pinned = places.filter(
      (p): p is MapPlace & { lat: number; lng: number } => p.lat != null && p.lng != null,
    );
    const map = new maplibregl.Map({
      container: container.current,
      style: OSM_STYLE,
      center: pinned.length ? [pinned[0].lng, pinned[0].lat] : [0, 20],
      zoom: pinned.length ? 11 : 1.5,
    });
    map.addControl(new maplibregl.NavigationControl(), "top-right");
    const bounds = new maplibregl.LngLatBounds();
    for (const p of pinned) {
      const popup = new maplibregl.Popup({ offset: 18 }).setHTML(
        `<strong>${escapeHtml(p.name)}</strong><br/>${escapeHtml(p.address)}<br/><a href="/item/${p.itemId}">${escapeHtml(p.itemTitle || "View item")}</a>`,
      );
      new maplibregl.Marker({ color: tokens.color.cobalt }).setLngLat([p.lng, p.lat]).setPopup(popup).addTo(map);
      bounds.extend([p.lng, p.lat]);
    }
    if (pinned.length > 1) map.fitBounds(bounds, { padding: 64, maxZoom: 13 });
    return () => map.remove();
  }, [places]);

  return <div ref={container} style={{ position: "absolute", inset: 0 }} />;
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

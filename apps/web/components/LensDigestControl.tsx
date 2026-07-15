"use client";

import { tokens } from "@openmind/ui";
import { useState } from "react";
import type { Lens } from "../lib/types";

const { color, font } = tokens;

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

type Frequency = "off" | "daily" | "weekly";

function parseSchedule(schedule: string): { frequency: Frequency; weekday: number } {
  if (schedule === "daily") return { frequency: "daily", weekday: 0 };
  if (schedule.startsWith("weekly:")) {
    const weekday = Number.parseInt(schedule.slice("weekly:".length), 10);
    return { frequency: "weekly", weekday: Number.isNaN(weekday) ? 0 : weekday };
  }
  return { frequency: "off", weekday: 0 };
}

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "recently";
  const secs = Math.round((Date.now() - then) / 1000);
  if (secs < 45) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.round(hrs / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.round(days / 30);
  return months < 12 ? `${months}mo ago` : `${Math.round(months / 12)}y ago`;
}

const selectStyle = {
  fontFamily: font.mono,
  fontSize: 11,
  letterSpacing: ".02em",
  color: color.ink,
  background: color.paper,
  border: `1px solid ${color.hairline}`,
  borderRadius: 8,
  padding: "5px 8px",
};

/** Lets a Lens owner set (or clear) an automatic digest cadence to Kindle. */
export function LensDigestControl({ lens }: { lens: Lens }) {
  const initial = parseSchedule(lens.digestSchedule ?? "");
  const [frequency, setFrequency] = useState<Frequency>(initial.frequency);
  const [weekday, setWeekday] = useState(initial.weekday);
  const [lastDigestAt, setLastDigestAt] = useState<string | null | undefined>(lens.lastDigestAt);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function scheduleFor(freq: Frequency, day: number): string {
    if (freq === "daily") return "daily";
    if (freq === "weekly") return `weekly:${day}`;
    return "";
  }

  async function save(nextFrequency: Frequency, nextWeekday: number) {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`/api/lenses/${lens.id}`, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          name: lens.name,
          rule: lens.rule,
          digestSchedule: scheduleFor(nextFrequency, nextWeekday),
        }),
      });
      if (!res.ok) throw new Error("save failed");
      const data = (await res.json()) as Lens;
      setFrequency(nextFrequency);
      setWeekday(nextWeekday);
      setLastDigestAt(data.lastDigestAt);
    } catch {
      setError("Couldn't update digest schedule.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
      <span className="meta" style={{ textTransform: "none", letterSpacing: ".02em", color: color.inkFaintAlt }}>
        Digest
      </span>
      <select
        aria-label="Digest frequency"
        value={frequency}
        disabled={busy}
        onChange={(e) => void save(e.target.value as Frequency, weekday)}
        style={selectStyle}
      >
        <option value="off">Off</option>
        <option value="daily">Daily</option>
        <option value="weekly">Weekly</option>
      </select>
      {frequency === "weekly" ? (
        <select
          aria-label="Digest weekday"
          value={weekday}
          disabled={busy}
          onChange={(e) => void save("weekly", Number.parseInt(e.target.value, 10))}
          style={selectStyle}
        >
          {WEEKDAYS.map((day, i) => (
            <option key={day} value={i}>
              {day}
            </option>
          ))}
        </select>
      ) : null}
      {error ? (
        <span style={{ fontFamily: font.mono, fontSize: 11, color: color.danger }} aria-live="polite">
          {error}
        </span>
      ) : lastDigestAt ? (
        <span style={{ fontFamily: font.mono, fontSize: 11, color: color.inkFaintAlt }}>
          digest last sent {relativeTime(lastDigestAt)}
        </span>
      ) : null}
    </span>
  );
}

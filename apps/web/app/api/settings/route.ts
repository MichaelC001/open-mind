import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  try {
    const res = await apiFetch("/settings", undefined, req);
    return new NextResponse(await res.text(), {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch (err) {
    console.error("settings GET proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

export async function PATCH(req: Request) {
  try {
    const body = await req.text();
    const res = await apiFetch("/settings", { method: "PATCH", body }, req);
    return new NextResponse(await res.text(), {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch (err) {
    console.error("settings PATCH proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

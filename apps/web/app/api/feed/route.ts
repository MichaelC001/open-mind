import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  const search = new URL(req.url).search;
  try {
    const res = await apiFetch(`/feed${search}`, undefined, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("feed GET proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

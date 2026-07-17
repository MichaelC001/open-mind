import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  try {
    const res = await apiFetch("/places", undefined, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("places GET proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

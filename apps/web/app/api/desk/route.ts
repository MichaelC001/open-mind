import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  try {
    const res = await apiFetch("/desk", undefined, req);
    return new NextResponse(await res.text(), {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch (err) {
    console.error("desk GET proxy failed", { err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

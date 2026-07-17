import { NextResponse } from "next/server";
import { apiFetch } from "../../../../../lib/api";

export async function GET(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await apiFetch(`/items/${id}/places`, undefined, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("places GET proxy failed", { itemId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

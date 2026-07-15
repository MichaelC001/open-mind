import { NextResponse } from "next/server";
import { apiFetch } from "../../../../../lib/api";

export async function GET(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await apiFetch(`/items/${id}/highlights`, undefined, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("highlights GET proxy failed", { itemId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

export async function POST(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const body = await req.text();
    const res = await apiFetch(`/items/${id}/highlights`, { method: "POST", body }, req);
    return new NextResponse(res.body, {
      status: res.status,
      headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
    });
  } catch (err) {
    console.error("highlights POST proxy failed", { itemId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

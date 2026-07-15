import { NextResponse } from "next/server";
import { apiFetch } from "../../../../lib/api";

export async function GET(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await apiFetch(`/lenses/${id}`, undefined, req);
    return new NextResponse(await res.text(), {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch (err) {
    console.error("lenses GET proxy failed", { lensId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

export async function PATCH(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const body = await req.text();
    const res = await apiFetch(`/lenses/${id}`, { method: "PATCH", body }, req);
    return new NextResponse(await res.text(), {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch (err) {
    console.error("lenses PATCH proxy failed", { lensId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

export async function DELETE(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await apiFetch(`/lenses/${id}`, { method: "DELETE" }, req);
    return new NextResponse(null, { status: res.status });
  } catch (err) {
    console.error("lenses DELETE proxy failed", { lensId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

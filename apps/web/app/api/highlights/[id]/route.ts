import { NextResponse } from "next/server";
import { apiFetch } from "../../../../lib/api";

export async function DELETE(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  try {
    const res = await apiFetch(`/highlights/${id}`, { method: "DELETE" }, req);
    return new NextResponse(null, { status: res.status });
  } catch (err) {
    console.error("highlights DELETE proxy failed", { itemId: id, err });
    return NextResponse.json({ error: "could not reach API" }, { status: 502 });
  }
}

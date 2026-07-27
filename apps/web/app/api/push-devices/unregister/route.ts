import { NextResponse } from "next/server";
import { apiFetch } from "../../../../lib/api";

export async function POST(req: Request) {
  const body = await req.text();
  const res = await apiFetch("/push-devices/unregister", { method: "POST", body }, req);
  return new NextResponse(null, { status: res.status });
}

import { NextResponse } from "next/server";
import { apiFetch } from "../../../lib/api";

export async function GET(req: Request) {
  const res = await apiFetch("/settings", undefined, req);
  return new NextResponse(await res.text(), {
    status: res.status,
    headers: { "content-type": "application/json" },
  });
}

export async function PATCH(req: Request) {
  const body = await req.text();
  const res = await apiFetch("/settings", { method: "PATCH", body }, req);
  return new NextResponse(await res.text(), {
    status: res.status,
    headers: { "content-type": "application/json" },
  });
}

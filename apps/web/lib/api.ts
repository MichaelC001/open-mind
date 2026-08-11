import "server-only";
import { cookies } from "next/headers";
import { auth } from "@clerk/nextjs/server";
import { authMode } from "./auth-mode";

const API_URL = process.env.API_URL ?? "http://localhost:8080";

async function sessionToken(): Promise<string | undefined> {
  if (authMode !== "clerk") {
    return (await cookies()).get("om_token")?.value;
  }
  // A server-component render can happen outside Clerk's request context
  // (or with no signed-in user); either way we must not throw — just send
  // no auth and let the API 401.
  try {
    const { getToken } = await auth();
    return (await getToken()) ?? undefined;
  } catch {
    return undefined;
  }
}

export async function apiFetch(path: string, init?: RequestInit, req?: Request): Promise<Response> {
  // A forwarded bearer wins outright, so resolve it first and skip the session
  // lookup entirely — the /api/* proxy routes are bearer-authed and were paying
  // for a Clerk auth() call on every request only to discard the result.
  const header = req?.headers.get("authorization");
  const token = header?.startsWith("Bearer ") ? header.slice(7) : await sessionToken();
  const headers = new Headers(init?.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  // Only force JSON for string bodies. FormData/streams (multipart uploads)
  // must keep their own content-type (incl. the multipart boundary).
  if (typeof init?.body === "string") headers.set("content-type", "application/json");
  return fetch(`${API_URL}${path}`, { ...init, headers, cache: "no-store" });
}

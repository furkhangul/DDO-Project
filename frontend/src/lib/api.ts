export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

export type Role = "reception" | "reservations" | "housekeeping" | "revenue" | "manager" | "guest";

export interface User {
  id: number;
  email: string;
  full_name: string;
  role: Role;
}

const STORAGE_KEY = "konakpro.session";

interface Session {
  access: string;
  refresh: string;
  user: User;
}

let session: Session | null = read();
let refreshing: Promise<boolean> | null = null;
const listeners = new Set<() => void>();

function read(): Session | null {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as Session;
  } catch {
    return null;
  }
}

function persist(next: Session | null) {
  session = next;
  if (next) localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  else localStorage.removeItem(STORAGE_KEY);
  listeners.forEach((fn) => fn());
}

export const currentUser = () => session?.user ?? null;
export const accessToken = () => session?.access ?? null;

export function onSessionChange(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

async function parseError(response: Response) {
  const body = await response.json().catch(() => null);
  return new ApiError(response.status, body?.error?.code ?? "UNKNOWN", body?.error?.message ?? "Sunucuya ulaşılamadı");
}

export async function login(email: string, password: string) {
  const response = await fetch("/api/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!response.ok) throw await parseError(response);
  const body = await response.json();
  persist({ access: body.tokens.access_token, refresh: body.tokens.refresh_token, user: body.user });
}

export async function logout() {
  const refresh = session?.refresh;
  persist(null);
  if (refresh) {
    await fetch("/api/v1/auth/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refresh }),
    }).catch(() => undefined);
  }
}

async function rotate() {
  if (!session) return false;
  const response = await fetch("/api/v1/auth/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: session.refresh }),
  });
  if (!response.ok) {
    persist(null);
    return false;
  }
  const body = await response.json();
  persist({ ...session, access: body.tokens.access_token, refresh: body.tokens.refresh_token });
  return true;
}

export function refreshSession() {
  refreshing ??= rotate().finally(() => {
    refreshing = null;
  });
  return refreshing;
}

export function idempotencyKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  idempotent?: boolean;
}

export async function api<T>(path: string, options: RequestOptions = {}, retried = false): Promise<T> {
  const headers: Record<string, string> = {};
  if (session) headers.Authorization = `Bearer ${session.access}`;
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  if (options.idempotent) headers["Idempotency-Key"] = idempotencyKey();
  const response = await fetch(`/api/v1${path}`, {
    method: options.method ?? "GET",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  if (response.status === 401 && session && !retried && (await refreshSession())) {
    return api<T>(path, options, true);
  }
  if (!response.ok) throw await parseError(response);
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

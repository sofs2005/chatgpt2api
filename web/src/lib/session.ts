"use client";

import { verifySession, type LoginResponse } from "@/lib/api";
import { clearAuthenticatedImageCache } from "@/lib/authenticated-image";
import {
  clearStoredAuthSession,
  getStoredAuthSession,
  setStoredAuthSession,
  type StoredAuthSession,
} from "@/store/auth";

let cachedAuthSession: StoredAuthSession | null | undefined;
let verifyAuthSessionPromise: Promise<StoredAuthSession | null> | null = null;
let authSessionVersion = 0;
export const AUTH_SESSION_CHANGE_EVENT = "chatgpt2api:auth-session-change";

export function authSessionFromLoginResponse(data: LoginResponse, key: string): StoredAuthSession {
  return {
    key,
    role: data.role,
    roleId: data.role_id,
    roleName: data.role_name,
    subjectId: data.subject_id,
    name: data.name,
    provider: data.provider,
    creationConcurrentLimit: data.creation_concurrent_limit,
    creationRpmLimit: data.creation_rpm_limit,
    billing: data.billing ?? null,
    menuPaths: data.menu_paths || [],
    apiPermissions: data.api_permissions || [],
    menus: data.menus || [],
  };
}

function emitAuthSessionChange() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(AUTH_SESSION_CHANGE_EVENT));
  }
}

export function getCachedAuthSession() {
  return cachedAuthSession;
}

export async function getVerifiedAuthSession(): Promise<StoredAuthSession | null> {
  if (cachedAuthSession !== undefined) {
    return cachedAuthSession;
  }

  const verifyStartedAtVersion = authSessionVersion;
  verifyAuthSessionPromise ??= verifyStoredAuthSession();
  try {
    const verifiedSession = await verifyAuthSessionPromise;
    if (verifyStartedAtVersion === authSessionVersion) {
      cachedAuthSession = verifiedSession;
      if (verifiedSession) {
        await setStoredAuthSession(verifiedSession);
      } else {
        clearAuthenticatedImageCache();
        await clearStoredAuthSession();
      }
      return verifiedSession;
    }
    return cachedAuthSession ?? null;
  } finally {
    if (verifyStartedAtVersion === authSessionVersion) {
      verifyAuthSessionPromise = null;
    }
  }
}

export async function setVerifiedAuthSession(session: StoredAuthSession) {
  authSessionVersion += 1;
  clearAuthenticatedImageCache();
  cachedAuthSession = session;
  verifyAuthSessionPromise = null;
  await setStoredAuthSession(session);
  emitAuthSessionChange();
}

export async function clearVerifiedAuthSession() {
  authSessionVersion += 1;
  clearAuthenticatedImageCache();
  cachedAuthSession = null;
  verifyAuthSessionPromise = null;
  await clearStoredAuthSession();
  emitAuthSessionChange();
}

async function verifyStoredAuthSession(): Promise<StoredAuthSession | null> {
  const storedSession = await getStoredAuthSession();
  if (!storedSession) {
    return null;
  }

  try {
    const data = await verifySession(storedSession.key);
    return authSessionFromLoginResponse(data, storedSession.key);
  } catch {
    return null;
  }
}

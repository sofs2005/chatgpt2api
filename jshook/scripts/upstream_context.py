"""Shared upstream session helper for jshook probes."""

import json
import os

from curl_cffi import requests


def _load_json_env(name: str) -> dict:
    raw = os.getenv(name, "").strip()
    if not raw:
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"{name} must be valid JSON") from exc
    return value if isinstance(value, dict) else {}


def build_upstream_session():
    fingerprint = _load_json_env("CHATGPT2API_UPSTREAM_FINGERPRINT_JSON")
    cookies = _load_json_env("CHATGPT2API_UPSTREAM_COOKIES_JSON")
    impersonate = str(fingerprint.get("impersonate") or "edge101").strip()
    session = requests.Session(impersonate=impersonate, verify=True)
    session.headers.update({
        "User-Agent": str(fingerprint.get("user-agent") or ""),
        "Sec-Ch-Ua": str(fingerprint.get("sec-ch-ua") or ""),
        "Sec-Ch-Ua-Mobile": str(fingerprint.get("sec-ch-ua-mobile") or "?0"),
        "Sec-Ch-Ua-Platform": str(fingerprint.get("sec-ch-ua-platform") or '"Windows"'),
    })
    for name, value in cookies.items():
        if value:
            session.cookies.set(name, str(value))
    return session

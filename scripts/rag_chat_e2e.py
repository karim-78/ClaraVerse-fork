#!/usr/bin/env python3
"""
RAG chat end-to-end test.

Connects to the chat WebSocket as an authenticated user, sends a
single chat_message with knowledge_project_ids attached, waits for
the assistant's reply, and asserts the marker phrase from the
attached project's knowledge base shows up in the reply. That proves
the full chat-side path:

  picker chips → ws payload → chat_service knowledge injection →
  LLM tool call → search_knowledge dispatch → rendered in reply

This is the chat-surface counterpart to scripts/rag_e2e.sh (which
covers the Nexus daemon surface). Run both for full coverage.

Requires the same backend + sidecars as rag_e2e.sh. The script
assumes the rag-e2e user + project already exist with the marker
doc indexed (run rag_e2e.sh once first to set that up).

Env:
  BASE_URL=http://localhost:3001   backend
  WS_URL=ws://localhost:3001       websocket (auto-derived if unset)
  EMAIL=rag-e2e@claraverse.local
  PASSWORD=RagTest1!
  TIMEOUT=180                      total budget for the reply
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import time
import urllib.request

try:
    import websockets
except ImportError:
    print("websockets package required: pip install websockets", file=sys.stderr)
    sys.exit(3)


BASE_URL = os.environ.get("BASE_URL", "http://localhost:3001").rstrip("/")
WS_URL = os.environ.get("WS_URL", BASE_URL.replace("http://", "ws://").replace("https://", "wss://"))
EMAIL = os.environ.get("EMAIL", "rag-e2e@claraverse.local")
PASSWORD = os.environ.get("PASSWORD", "RagTest1!")
TIMEOUT = int(os.environ.get("TIMEOUT", "180"))
MARKER = "BLUEMORPHO-XJ47"  # must match what rag_e2e.sh uploads


def http_post(path: str, body: dict, token: str | None = None) -> dict:
    req = urllib.request.Request(
        f"{BASE_URL}{path}",
        method="POST",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def http_get(path: str, token: str) -> dict:
    req = urllib.request.Request(
        f"{BASE_URL}{path}",
        headers={"Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def login() -> tuple[str, str]:
    """Returns (access_token, user_id). Logs in (assumes user exists)."""
    body = {"email": EMAIL, "password": PASSWORD}
    try:
        resp = http_post("/api/auth/login", body)
    except urllib.error.HTTPError as e:
        if e.code == 401:
            print(
                "❌ Login failed — run scripts/rag_e2e.sh first to create the user",
                file=sys.stderr,
            )
            sys.exit(3)
        raise
    return resp["access_token"], resp["user"]["id"]


def find_project(token: str) -> str:
    """Returns the rag-e2e project ID. Asserts it has at least one file."""
    projects = http_get("/api/nexus/projects", token)
    for p in projects:
        if p["name"] == "rag-e2e":
            files = http_get(f"/api/projects/{p['id']}/knowledge/files", token)
            ready = [f for f in files.get("files", []) if f.get("status") == "ready"]
            if not ready:
                print(
                    f"❌ Project rag-e2e has no ready files. "
                    f"Run scripts/rag_e2e.sh to seed.",
                    file=sys.stderr,
                )
                sys.exit(3)
            print(f"   project: {p['id']}  files_ready: {len(ready)}")
            return p["id"]
    print("❌ No 'rag-e2e' project found. Run scripts/rag_e2e.sh first.", file=sys.stderr)
    sys.exit(3)


async def chat_with_knowledge(token: str, project_id: str) -> str:
    """Opens a WS connection, sends a chat message with knowledge attached,
    accumulates the assistant reply, returns the final content."""
    ws_url = f"{WS_URL}/ws/chat?token={token}"
    print(f"   ws: {ws_url}")

    convo_id = f"rag-chat-test-{int(time.time())}"
    prompt = (
        "What's the internal codename for the Q9 launch in this project? "
        "Cite the source file."
    )
    payload = {
        "type": "chat_message",
        "conversation_id": convo_id,
        "content": prompt,
        "history": [],
        "knowledge_project_ids": [project_id],
    }

    assistant_text: list[str] = []
    tool_calls: list[str] = []
    saw_search_knowledge = False
    saw_done = False

    deadline = time.time() + TIMEOUT
    async with websockets.connect(ws_url, max_size=10 * 1024 * 1024) as ws:
        await ws.send(json.dumps(payload))
        print(f"   sent prompt: {prompt!r}")

        while time.time() < deadline:
            try:
                raw = await asyncio.wait_for(ws.recv(), timeout=deadline - time.time())
            except asyncio.TimeoutError:
                break
            try:
                msg = json.loads(raw)
            except json.JSONDecodeError:
                continue

            mtype = msg.get("type", "")
            # Accumulate streamed content
            if mtype == "chunk":
                if "content" in msg:
                    assistant_text.append(msg["content"])
            elif mtype == "tool_call_start" or mtype == "tool_call":
                name = msg.get("tool_name") or msg.get("name") or "?"
                tool_calls.append(name)
                if name == "search_knowledge":
                    saw_search_knowledge = True
                    print(f"   ✓ saw tool_call: search_knowledge")
            elif mtype == "tool_result":
                # interesting for debugging
                name = msg.get("tool_name") or "?"
                if name == "search_knowledge":
                    print(f"   ✓ search_knowledge returned a result")
            elif mtype == "done" or mtype == "stream_end" or mtype == "complete":
                saw_done = True
                break
            elif mtype == "error":
                print(f"   ✗ server error: {msg.get('error') or msg}")
                break

    return "".join(assistant_text), tool_calls, saw_search_knowledge, saw_done


def main() -> int:
    print("🧪 RAG chat e2e test")
    print(f"   backend: {BASE_URL}")
    print(f"   marker:  {MARKER}")
    print()

    print("── Login ──")
    token, user_id = login()
    print(f"   user_id: {user_id}")

    print("── Find seeded project ──")
    project_id = find_project(token)

    print("── Chat with knowledge attached ──")
    reply, tools, used_sk, done = asyncio.run(chat_with_knowledge(token, project_id))

    print()
    print(f"   stream_done={done}  tools_called={tools}")
    print(f"   reply (first 400 chars):")
    print("   " + reply[:400].replace("\n", "\n   "))
    print()

    failed = False
    if not used_sk:
        print("   ✗ search_knowledge was NOT called by the model")
        failed = True
    if MARKER not in reply:
        print(f"   ✗ marker {MARKER!r} missing from reply")
        failed = True

    if failed:
        print()
        print("❌ Chat RAG path FAILED")
        return 1

    print()
    print("✅ Chat RAG path PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())

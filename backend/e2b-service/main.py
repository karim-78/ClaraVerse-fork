#!/usr/bin/env python3
"""
E2B Code Executor Microservice for ClaraVerse / ClaraVerse
Provides REST API for executing Python code in E2B sandboxes.

Sandboxes are pooled per conversation_id — the first call for a conv spins
a fresh sandbox, subsequent calls within an idle window reconnect to the
same one so dataframes, imports, and variables persist across turns. Idle
or hard-deadline sandboxes are evicted by a background sweep.

Header `X-Conversation-Id` (or top-level `conversation_id` field) controls
pooling. Without it, the sandbox is ephemeral (legacy behavior).
"""

import asyncio
import base64
import logging
import os
import threading
import time
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

from e2b_code_interpreter import Sandbox
from fastapi import FastAPI, File, Form, HTTPException, Request, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

E2B_API_KEY = os.getenv("E2B_API_KEY", "")

# Pool tuning. Each open sandbox costs E2B sandbox-hours, so we evict
# aggressively on the assumption that a conversation gone quiet for >15min
# is done. Hard cap keeps a single conversation from holding one open all day.
SANDBOX_IDLE_TIMEOUT_SEC = int(os.getenv("E2B_IDLE_TIMEOUT_SEC", "900"))   # 15 min
SANDBOX_HARD_TIMEOUT_SEC = int(os.getenv("E2B_HARD_TIMEOUT_SEC", "3600"))  # 60 min
SANDBOX_TIMEOUT_HEADROOM = 60  # tell E2B to keep sandbox alive a little
                              # past our idle timer so reconnects always work


@dataclass
class PooledSandbox:
    sandbox_id: str
    api_key: str
    created_at: float
    last_used: float
    conversation_id: str


_pool: Dict[str, PooledSandbox] = {}
_pool_lock = threading.RLock()


def _connect_or_create(api_key: str, prior: Optional[PooledSandbox] = None) -> Sandbox:
    """Return a live Sandbox, reconnecting to `prior` if possible."""
    if prior is not None:
        try:
            sb = Sandbox.connect(prior.sandbox_id, api_key=api_key)
            sb.set_timeout(SANDBOX_IDLE_TIMEOUT_SEC + SANDBOX_TIMEOUT_HEADROOM)
            return sb
        except Exception as exc:
            logger.warning(
                "Reconnect to sandbox %s failed (%s); creating a fresh one",
                prior.sandbox_id, exc,
            )
    sb = Sandbox.create(api_key=api_key)
    sb.set_timeout(SANDBOX_IDLE_TIMEOUT_SEC + SANDBOX_TIMEOUT_HEADROOM)
    return sb


@contextmanager
def lease_sandbox(api_key: str, conversation_id: Optional[str]):
    """Yield a Sandbox.

    - With conversation_id: pooled per conversation. Sandbox stays alive in
      E2B after this returns; the next request for the same conv reconnects.
    - Without: ephemeral. Sandbox is killed on context exit.
    """
    if not api_key:
        raise HTTPException(
            status_code=400,
            detail="E2B API key not configured. Set it via E2B_API_KEY env var "
                   "or X-E2B-API-Key header.",
        )

    if not conversation_id:
        sandbox = Sandbox.create(api_key=api_key)
        try:
            yield sandbox
        finally:
            try:
                sandbox.kill()
            except Exception:
                pass
        return

    with _pool_lock:
        prior = _pool.get(conversation_id)

    sandbox = _connect_or_create(api_key, prior)

    with _pool_lock:
        _pool[conversation_id] = PooledSandbox(
            sandbox_id=sandbox.sandbox_id,
            api_key=api_key,
            created_at=prior.created_at if prior else time.time(),
            last_used=time.time(),
            conversation_id=conversation_id,
        )

    try:
        yield sandbox
    finally:
        with _pool_lock:
            entry = _pool.get(conversation_id)
            if entry and entry.sandbox_id == sandbox.sandbox_id:
                entry.last_used = time.time()
        # Do NOT kill — the pool keeps it alive.


def _evict_loop():
    """Background thread: kill sandboxes idle past IDLE or alive past HARD."""
    while True:
        time.sleep(60)
        now = time.time()
        with _pool_lock:
            stale = [
                k for k, v in _pool.items()
                if (now - v.last_used) > SANDBOX_IDLE_TIMEOUT_SEC
                or (now - v.created_at) > SANDBOX_HARD_TIMEOUT_SEC
            ]
            entries = [_pool.pop(k) for k in stale]
        for entry in entries:
            try:
                sb = Sandbox.connect(entry.sandbox_id, api_key=entry.api_key)
                sb.kill()
                logger.info(
                    "Evicted sandbox %s for conv %s (age=%ds, idle=%ds)",
                    entry.sandbox_id, entry.conversation_id,
                    int(time.time() - entry.created_at),
                    int(time.time() - entry.last_used),
                )
            except Exception as exc:
                logger.warning(
                    "Eviction kill failed for %s: %s", entry.sandbox_id, exc,
                )


app = FastAPI(
    title="E2B Code Executor Service",
    description="Microservice for executing Python code in pooled E2B sandboxes.",
    version="2.0.0",
)
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.on_event("startup")
def _start_eviction():
    threading.Thread(target=_evict_loop, daemon=True, name="e2b-evict").start()
    logger.info(
        "Sandbox pool armed (idle=%ds, hard=%ds)",
        SANDBOX_IDLE_TIMEOUT_SEC, SANDBOX_HARD_TIMEOUT_SEC,
    )


# ─── Request / response shapes ───────────────────────────────────────────

class ExecuteRequest(BaseModel):
    code: str
    timeout: Optional[int] = 30
    conversation_id: Optional[str] = None


class PlotResult(BaseModel):
    format: str
    data: str


class ExecuteResponse(BaseModel):
    success: bool
    stdout: str
    stderr: str
    error: Optional[str] = None
    plots: List[PlotResult] = []
    execution_time: Optional[float] = None
    sandbox_id: Optional[str] = None
    sandbox_reused: bool = False


class AdvancedExecuteRequest(BaseModel):
    code: str
    timeout: Optional[int] = 30
    dependencies: List[str] = []
    output_files: List[str] = []
    conversation_id: Optional[str] = None


class FileResult(BaseModel):
    filename: str
    data: str
    size: int


class AdvancedExecuteResponse(BaseModel):
    success: bool
    stdout: str
    stderr: str
    error: Optional[str] = None
    plots: List[PlotResult] = []
    files: List[FileResult] = []
    execution_time: Optional[float] = None
    install_output: str = ""
    sandbox_id: Optional[str] = None
    sandbox_reused: bool = False


# ─── helpers ─────────────────────────────────────────────────────────────

def get_api_key(request: Request) -> str:
    key = request.headers.get("X-E2B-API-Key", "").strip()
    if key:
        return key
    if E2B_API_KEY:
        return E2B_API_KEY
    return ""


def get_conversation_id(request: Request, body_conv_id: Optional[str]) -> Optional[str]:
    """Header wins, falls back to body field."""
    header = request.headers.get("X-Conversation-Id", "").strip()
    if header:
        return header
    if body_conv_id and body_conv_id.strip():
        return body_conv_id.strip()
    return None


def _was_reused(conversation_id: Optional[str]) -> bool:
    if not conversation_id:
        return False
    with _pool_lock:
        entry = _pool.get(conversation_id)
    if not entry:
        return False
    # Considered "reused" if this entry existed before this call. We can't
    # tell perfectly because we set last_used during lease — fall back to a
    # heuristic: if created_at < now-2s, the sandbox was around already.
    return (time.time() - entry.created_at) > 2


def _collect_logs(execution) -> tuple[str, str, Optional[str]]:
    stdout = "\n".join(execution.logs.stdout) if execution.logs.stdout else ""
    stderr = "\n".join(execution.logs.stderr) if execution.logs.stderr else ""
    error_msg = str(execution.error) if execution.error else None
    return stdout, stderr, error_msg


def _collect_plots_and_texts(execution) -> tuple[List[PlotResult], List[str]]:
    plots: List[PlotResult] = []
    texts: List[str] = []
    for i, result in enumerate(execution.results):
        if hasattr(result, "png") and result.png:
            plots.append(PlotResult(format="png", data=result.png))
            logger.info("Found plot %d", i)
        elif hasattr(result, "text") and result.text:
            texts.append(result.text)
            logger.info("Found text result %d", i)
    return plots, texts


# ─── endpoints ───────────────────────────────────────────────────────────

@app.get("/health")
async def health_check(request: Request):
    key = get_api_key(request)
    with _pool_lock:
        active = len(_pool)
    return {
        "status": "healthy",
        "service": "e2b-executor",
        "e2b_api_key_set": bool(key),
        "active_sandboxes": active,
    }


@app.get("/sandboxes")
async def list_sandboxes():
    """Inspection only. Useful when debugging the pool."""
    with _pool_lock:
        return [
            {
                "conversation_id": v.conversation_id,
                "sandbox_id": v.sandbox_id,
                "age_sec": int(time.time() - v.created_at),
                "idle_sec": int(time.time() - v.last_used),
            }
            for v in _pool.values()
        ]


@app.delete("/sandboxes/{conversation_id}")
async def kill_sandbox(conversation_id: str):
    """Explicitly tear down a conversation's sandbox."""
    with _pool_lock:
        entry = _pool.pop(conversation_id, None)
    if not entry:
        return {"killed": False, "reason": "no sandbox for conversation"}
    try:
        sb = Sandbox.connect(entry.sandbox_id, api_key=entry.api_key)
        sb.kill()
        return {"killed": True, "sandbox_id": entry.sandbox_id}
    except Exception as exc:
        return {"killed": False, "reason": str(exc)}


@app.post("/execute", response_model=ExecuteResponse)
async def execute_code(request: ExecuteRequest, raw_request: Request):
    api_key = get_api_key(raw_request)
    conversation_id = get_conversation_id(raw_request, request.conversation_id)
    logger.info(
        "Executing code (length=%d conv=%s)",
        len(request.code), conversation_id or "<ephemeral>",
    )

    was_reused = _was_reused(conversation_id)
    try:
        with lease_sandbox(api_key, conversation_id) as sandbox:
            execution = sandbox.run_code(request.code)
            stdout, stderr, error_msg = _collect_logs(execution)
            plots, texts = _collect_plots_and_texts(execution)
            if texts:
                tail = "\n".join(texts)
                stdout = (stdout + "\n" + tail) if stdout else tail
            return ExecuteResponse(
                success=error_msg is None,
                stdout=stdout,
                stderr=stderr,
                error=error_msg,
                plots=plots,
                sandbox_id=sandbox.sandbox_id,
                sandbox_reused=was_reused,
            )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Sandbox execution failed: %s", exc)
        raise HTTPException(status_code=500, detail=f"Sandbox execution failed: {exc}")


@app.post("/execute-with-files", response_model=ExecuteResponse)
async def execute_with_files(
    raw_request: Request,
    code: str = Form(...),
    files: List[UploadFile] = File(...),
    timeout: int = Form(30),
    conversation_id: Optional[str] = Form(None),
):
    api_key = get_api_key(raw_request)
    conv_id = get_conversation_id(raw_request, conversation_id)
    logger.info(
        "Executing code with %d files (conv=%s)",
        len(files), conv_id or "<ephemeral>",
    )
    was_reused = _was_reused(conv_id)
    try:
        with lease_sandbox(api_key, conv_id) as sandbox:
            for file in files:
                content = await file.read()
                sandbox.files.write(file.filename, content)
                logger.info("Uploaded file: %s (%d bytes)", file.filename, len(content))
            execution = sandbox.run_code(code)
            stdout, stderr, error_msg = _collect_logs(execution)
            plots, _ = _collect_plots_and_texts(execution)
            return ExecuteResponse(
                success=error_msg is None,
                stdout=stdout,
                stderr=stderr,
                error=error_msg,
                plots=plots,
                sandbox_id=sandbox.sandbox_id,
                sandbox_reused=was_reused,
            )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Sandbox execution failed: %s", exc)
        raise HTTPException(status_code=500, detail=f"Sandbox execution failed: {exc}")


@app.post("/execute-advanced", response_model=AdvancedExecuteResponse)
async def execute_advanced(request: AdvancedExecuteRequest, raw_request: Request):
    """Pip install + run + auto-detect new files. Pooled by conversation_id."""
    api_key = get_api_key(raw_request)
    conv_id = get_conversation_id(raw_request, request.conversation_id)
    logger.info(
        "Advanced exec: code=%d chars deps=%s out=%s conv=%s",
        len(request.code), request.dependencies, request.output_files,
        conv_id or "<ephemeral>",
    )
    was_reused = _was_reused(conv_id)

    try:
        with lease_sandbox(api_key, conv_id) as sandbox:
            start_time = time.time()
            install_output = ""

            # 1. Install dependencies
            if request.dependencies:
                deps_str = " ".join(request.dependencies)
                logger.info("Installing dependencies: %s", deps_str)
                try:
                    result = sandbox.commands.run(f"pip install -q {deps_str}", timeout=60)
                    install_output = (result.stdout or "") + (result.stderr or "")
                except Exception as exc:
                    logger.error("Dependency install failed: %s", exc)
                    return AdvancedExecuteResponse(
                        success=False,
                        stdout="",
                        stderr="",
                        error=f"Failed to install dependencies: {exc}",
                        plots=[],
                        files=[],
                        execution_time=time.time() - start_time,
                        install_output=str(exc),
                        sandbox_id=sandbox.sandbox_id,
                        sandbox_reused=was_reused,
                    )

            # 2. Snapshot files before
            files_before: set = set()
            try:
                snap = sandbox.commands.run(
                    "find /home/user -maxdepth 2 -type f 2>/dev/null || ls -la /home/user",
                    timeout=10,
                )
                if snap.stdout:
                    for line in snap.stdout.strip().split("\n"):
                        line = line.strip()
                        if line and not line.startswith("total"):
                            if line.startswith("/"):
                                files_before.add(line)
                            else:
                                parts = line.split()
                                if len(parts) >= 9:
                                    files_before.add(parts[-1])
            except Exception as exc:
                logger.warning("Pre-snapshot failed: %s", exc)

            # 3. Run user code
            execution = sandbox.run_code(request.code)
            stdout, stderr, error_msg = _collect_logs(execution)
            plots, texts = _collect_plots_and_texts(execution)
            if texts:
                tail = "\n".join(texts)
                stdout = (stdout + "\n" + tail) if stdout else tail

            # 4. Detect new files
            files_after: set = set()
            new_files: List[str] = []
            try:
                snap = sandbox.commands.run(
                    "find /home/user -maxdepth 2 -type f 2>/dev/null || ls -la /home/user",
                    timeout=10,
                )
                if snap.stdout:
                    for line in snap.stdout.strip().split("\n"):
                        line = line.strip()
                        if line and not line.startswith("total"):
                            if line.startswith("/"):
                                files_after.add(line)
                            else:
                                parts = line.split()
                                if len(parts) >= 9:
                                    files_after.add(parts[-1])
                new_files = list(files_after - files_before)
                excluded = (".pyc", "__pycache__", ".ipynb_checkpoints", ".cache")
                new_files = [f for f in new_files if not any(p in f for p in excluded)]
            except Exception as exc:
                logger.warning("Post-snapshot failed: %s", exc)

            # 5. Collect output files
            files: List[FileResult] = []
            collected: set = set()
            for filepath in request.output_files:
                try:
                    content = sandbox.files.read(filepath)
                    if isinstance(content, str):
                        content = content.encode("utf-8")
                    filename = os.path.basename(filepath)
                    files.append(FileResult(
                        filename=filename,
                        data=base64.b64encode(content).decode("utf-8"),
                        size=len(content),
                    ))
                    collected.add(filename)
                except Exception as exc:
                    logger.warning("Could not retrieve %s: %s", filepath, exc)

            for filepath in new_files:
                filename = os.path.basename(filepath)
                if filename in collected:
                    continue
                content = None
                for candidate in (filepath, f"/home/user/{filename}", filename):
                    try:
                        content = sandbox.files.read(candidate)
                        break
                    except Exception:
                        continue
                if content is None:
                    continue
                if isinstance(content, str):
                    content = content.encode("utf-8")
                files.append(FileResult(
                    filename=filename,
                    data=base64.b64encode(content).decode("utf-8"),
                    size=len(content),
                ))
                collected.add(filename)

            return AdvancedExecuteResponse(
                success=error_msg is None,
                stdout=stdout,
                stderr=stderr,
                error=error_msg,
                plots=plots,
                files=files,
                execution_time=time.time() - start_time,
                install_output=install_output,
                sandbox_id=sandbox.sandbox_id,
                sandbox_reused=was_reused,
            )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Advanced sandbox execution failed: %s", exc)
        raise HTTPException(status_code=500, detail=f"Sandbox execution failed: {exc}")


# Upload a file directly into a pooled sandbox (Tier-2 prereq).
class FilePushRequest(BaseModel):
    conversation_id: str
    target_path: str   # e.g. "/data/sales.csv"
    content_b64: str


@app.post("/files/push")
async def push_file(request: FilePushRequest, raw_request: Request):
    api_key = get_api_key(raw_request)
    if not request.conversation_id:
        raise HTTPException(status_code=400, detail="conversation_id required")
    try:
        with lease_sandbox(api_key, request.conversation_id) as sandbox:
            try:
                payload = base64.b64decode(request.content_b64)
            except Exception as exc:
                raise HTTPException(status_code=400, detail=f"invalid base64: {exc}")
            # Make sure /data exists for the common case
            sandbox.commands.run("mkdir -p /data", timeout=5)
            sandbox.files.write(request.target_path, payload)
            return {"ok": True, "path": request.target_path, "size": len(payload), "sandbox_id": sandbox.sandbox_id}
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("File push failed: %s", exc)
        raise HTTPException(status_code=500, detail=f"file push failed: {exc}")


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8001, log_level="info")

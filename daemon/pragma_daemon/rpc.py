import asyncio
import json
import logging
import os
from typing import Callable

logger = logging.getLogger(__name__)

class RPCServer:
    def __init__(self, socket_path: str, max_concurrency: int = 20):
        self.socket_path = socket_path
        self.handlers: dict[str, Callable] = {}
        self._writer_lock = asyncio.Lock()
        # Server-side cap on concurrent handler execution. Without this, a burst
        # of parallel codegen requests spawns unbounded asyncio tasks that can
        # overwhelm the provider and exhaust file descriptors.
        self._semaphore = asyncio.Semaphore(max_concurrency)

    def register(self, method: str, handler: Callable) -> None:
        self.handlers[method] = handler

    async def serve(self) -> None:
        # Ensure the socket directory exists and no stale socket remains —
        # otherwise sock.bind() raises FileNotFoundError / AddressInUse.
        sock_dir = os.path.dirname(self.socket_path)
        if sock_dir:
            os.makedirs(sock_dir, exist_ok=True)
        if os.path.exists(self.socket_path):
            try:
                os.remove(self.socket_path)
            except OSError:
                pass

        server = await asyncio.start_unix_server(
            self._handle_connection, path=self.socket_path
        )
        logger.info(f"RPCServer listening on {self.socket_path}")
        async with server:
            await server.serve_forever()

    async def _handle_connection(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        logger.info("Client connected")
        # Track active tasks to cancel them on disconnect
        active_tasks: set[asyncio.Task] = set()
        # Cap max line size to prevent memory exhaustion DoS (10 MB)
        MAX_LINE_SIZE = 10 * 1024 * 1024
        try:
            while True:
                line = await reader.readline(MAX_LINE_SIZE)
                if not line:
                    break
                if len(line) >= MAX_LINE_SIZE:
                    logger.warning("Client sent line exceeding max size, closing connection")
                    await self._send(writer, {"error": {"code": -32700, "message": "Line too large"}})
                    break
                try:
                    request = json.loads(line)
                    task = asyncio.create_task(self._dispatch(writer, request))
                    active_tasks.add(task)
                    # Remove task from set when done
                    task.add_done_callback(active_tasks.discard)
                except json.JSONDecodeError:
                    await self._send(writer, {"error": {"code": -32700, "message": "Parse error"}})
        except asyncio.CancelledError:
            pass
        except (ConnectionResetError, BrokenPipeError):
            pass
        finally:
            # Cancel all active tasks before closing writer
            for task in list(active_tasks):
                task.cancel()
            # Wait for tasks to finish (with timeout)
            if active_tasks:
                await asyncio.wait(active_tasks, timeout=5.0)
            writer.close()
            try:
                await writer.wait_closed()
            except (ConnectionResetError, BrokenPipeError):
                pass
            logger.info("Client disconnected")

    async def _dispatch(self, writer: asyncio.StreamWriter, request: dict):
        req_id = request.get("id")
        method = request.get("method")
        params = request.get("params")

        if not method or method not in self.handlers:
            if req_id is not None:
                await self._send(writer, {
                    "id": req_id,
                    "error": {"code": -32601, "message": f"Method not found: {method}"}
                })
            return

        async with self._semaphore:
            try:
                handler = self.handlers[method]
                # Normalise params: JSON-RPC may send an object (kwargs), an
                # array (positional), or null/absent (no args). The Go client
                # sends `null` for no-arg calls like ping — previously this was
                # passed positionally and broke every no-arg method.
                if isinstance(params, dict):
                    result = await handler(**params)
                elif isinstance(params, list):
                    result = await handler(*params)
                else:
                    result = await handler()
                if req_id is not None:
                    await self._send(writer, {"id": req_id, "result": result})
            except Exception as e:
                logger.exception(f"Error handling {method}")
                if req_id is not None:
                    try:
                        await self._send(writer, {
                            "id": req_id,
                            "error": {"code": -32000, "message": str(e)}
                        })
                    except (ConnectionResetError, BrokenPipeError):
                        pass

    async def _send(self, writer: asyncio.StreamWriter, response: dict):
        data = json.dumps(response) + "\n"
        async with self._writer_lock:
            writer.write(data.encode("utf-8"))
            await writer.drain()

import hashlib
import json
import logging
import threading
import time
from collections import OrderedDict
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


class L1Cache:
    """Exact-hash L1 cache with batched persistence.

    Keyed by an arbitrary string (callers build a descriptive key, e.g.
    ``"interview_chat:" + json.dumps(messages)``). The key is hashed
    internally so memory stays bounded regardless of key length. Values are
    any JSON-serialisable object (we store the full RPC result dict).

    Persistence is batched: writes are deferred and flushed periodically
    (every 5 seconds when dirty) or on explicit flush/close. This avoids
    20+ disk writes per pipeline run.
    """

    def __init__(self, max_entries: int = 500, persist_path: Path | None = None):
        self.max_entries = max_entries
        self.persist_path = persist_path
        self._cache: "OrderedDict[str, Any]" = OrderedDict()
        self._dirty = False
        self._flush_lock = threading.Lock()
        self._flush_timer: threading.Timer | None = None
        if self.persist_path:
            self.load()

    @staticmethod
    def _hash(key: str) -> str:
        return hashlib.sha256(key.encode("utf-8")).hexdigest()

    def get(self, key: str) -> Any | None:
        h = self._hash(key)
        if h in self._cache:
            self._cache.move_to_end(h)
            return self._cache[h]
        return None

    def set(self, key: str, value: Any) -> None:
        h = self._hash(key)
        self._cache[h] = value
        self._cache.move_to_end(h)
        if len(self._cache) > self.max_entries:
            self._cache.popitem(last=False)
        self._dirty = True
        self._schedule_flush()

    # Backwards-compatible alias.
    def put(self, key: str, value: Any) -> None:
        self.set(key, value)

    def _schedule_flush(self) -> None:
        """Schedule a flush in 5 seconds if one isn't already pending."""
        if self._flush_timer is not None:
            return  # Already scheduled
        self._flush_timer = threading.Timer(5.0, self._do_flush)
        self._flush_timer.daemon = True
        self._flush_timer.start()

    def _do_flush(self) -> None:
        """Flush dirty cache to disk. Called by timer."""
        with self._flush_lock:
            self._flush_timer = None
            if self._dirty:
                self._save_locked()

    def flush(self) -> None:
        """Immediately flush dirty cache to disk."""
        with self._flush_lock:
            if self._flush_timer is not None:
                self._flush_timer.cancel()
                self._flush_timer = None
            if self._dirty:
                self._save_locked()

    def _save_locked(self) -> None:
        """Write cache to disk. Must be called with _flush_lock held."""
        if not self.persist_path:
            return
        try:
            self.persist_path.parent.mkdir(parents=True, exist_ok=True)
            temp_path = self.persist_path.with_suffix('.tmp')
            with open(temp_path, "w") as f:
                json.dump(list(self._cache.items()), f)
            temp_path.replace(self.persist_path)
            self._dirty = False
        except Exception as e:
            logger.warning(f"Failed to save cache: {e}")

    def save(self) -> None:
        """Compatibility shim — use flush() for explicit persistence."""
        self.flush()

    def load(self) -> None:
        if not self.persist_path or not self.persist_path.exists():
            return
        try:
            with open(self.persist_path, "r") as f:
                items = json.load(f)
                self._cache = OrderedDict(items)
            while len(self._cache) > self.max_entries:
                self._cache.popitem(last=False)
        except Exception as e:
            logger.warning(f"Failed to load cache: {e}")

    def clear(self) -> None:
        self._cache.clear()
        self.flush()

    @property
    def size(self) -> int:
        return len(self._cache)

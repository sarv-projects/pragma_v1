import hashlib
import json
import logging
from collections import OrderedDict
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


class L1Cache:
    """Exact-hash L1 cache.

    Keyed by an arbitrary string (callers build a descriptive key, e.g.
    ``"interview_chat:" + json.dumps(messages)``). The key is hashed
    internally so memory stays bounded regardless of key length. Values are
    any JSON-serialisable object (we store the full RPC result dict).
    """

    def __init__(self, max_entries: int = 500, persist_path: Path | None = None):
        self.max_entries = max_entries
        self.persist_path = persist_path
        self._cache: "OrderedDict[str, Any]" = OrderedDict()
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
        self.save()

    # Backwards-compatible alias.
    def put(self, key: str, value: Any) -> None:
        self.set(key, value)

    def save(self) -> None:
        if not self.persist_path:
            return
        try:
            self.persist_path.parent.mkdir(parents=True, exist_ok=True)
            # Write to temp file first, then atomically rename to avoid corruption
            temp_path = self.persist_path.with_suffix('.tmp')
            with open(temp_path, "w") as f:
                json.dump(list(self._cache.items()), f)
            temp_path.replace(self.persist_path)
        except Exception as e:
            logger.warning(f"Failed to save cache: {e}")

    def load(self) -> None:
        if not self.persist_path or not self.persist_path.exists():
            return
        try:
            with open(self.persist_path, "r") as f:
                items = json.load(f)
                self._cache = OrderedDict(items)
            # Trim if a previously-larger cache file is loaded under a smaller
            # max_entries, so the in-memory cache never exceeds the cap.
            while len(self._cache) > self.max_entries:
                self._cache.popitem(last=False)
        except Exception as e:
            logger.warning(f"Failed to load cache: {e}")

    def clear(self) -> None:
        self._cache.clear()
        self.save()

    @property
    def size(self) -> int:
        return len(self._cache)

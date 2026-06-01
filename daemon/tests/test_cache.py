from pragma_daemon.cache import L1Cache


def test_set_get_string_key():
    c = L1Cache()
    assert c.get("missing") is None
    c.set("interview_chat:abc", {"content": "hi", "done": False})
    assert c.get("interview_chat:abc") == {"content": "hi", "done": False}


def test_put_alias():
    c = L1Cache()
    c.put("k", {"x": 1})
    assert c.get("k") == {"x": 1}


def test_lru_eviction():
    c = L1Cache(max_entries=2)
    c.set("a", 1)
    c.set("b", 2)
    c.set("c", 3)  # evicts the least-recently-used ("a")
    assert c.get("a") is None
    assert c.get("b") == 2
    assert c.get("c") == 3


def test_persistence(tmp_path):
    p = tmp_path / "cache.json"
    c = L1Cache(persist_path=p)
    c.set("k", {"v": "stored"})
    c2 = L1Cache(persist_path=p)
    assert c2.get("k") == {"v": "stored"}

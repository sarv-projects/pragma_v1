from pragma_daemon.research import _extract_entities


def test_word_boundary_avoids_false_positive():
    # "credis" must NOT match the entity "redis" (E8).
    manifest = {"description": "a credis-based widget store"}
    assert "redis" not in _extract_entities(manifest)


def test_detects_real_entity():
    manifest = {"description": "Payment API using Stripe and FastAPI"}
    found = _extract_entities(manifest)
    assert "stripe" in found
    assert "fastapi" in found


def test_detects_dotted_entity():
    manifest = {"description": "Build a Next.js app"}
    found = _extract_entities(manifest)
    assert "next.js" in found or "nextjs" in found


def test_no_entities_in_plain_text():
    manifest = {"description": "a simple counter that increments a number"}
    assert _extract_entities(manifest) == []

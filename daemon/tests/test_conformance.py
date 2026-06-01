from pragma_daemon.conformance import check_conformance, strip_code_fences


def test_strip_fenced_block():
    text = "Here is the code:\n```python\nprint(1)\n```\n"
    assert strip_code_fences(text) == "print(1)"


def test_strip_leading_fence_only():
    text = "```\nx = 1\n"
    assert "x = 1" in strip_code_fences(text)


def test_strip_no_fence_passthrough():
    text = "def f():\n    return 1\n"
    assert strip_code_fences(text) == text


def test_valid_python_no_violations():
    code = "def add(a, b):\n    return a + b\n"
    violations = check_conformance(code, "python", {})
    assert all(v.rule != "syntax_error" for v in violations)


def test_broken_python_syntax_error():
    code = "def add(a, b)\n    return a + b\n"  # missing colon
    violations = check_conformance(code, "python", {})
    assert any(v.rule == "syntax_error" for v in violations)


def test_ban_any_type_typescript():
    code = "const x: any = 1;\n"
    violations = check_conformance(code, "typescript", {"ban_any_type": True})
    assert any(v.rule == "ban_any_type" for v in violations)

from pragma_daemon.spec_validator import validate_spec, fatal_errors


def test_detect_cycles():
    spec = {
        "files": [
            {"path": "a.py", "depends_on": ["b.py"]},
            {"path": "b.py", "depends_on": ["c.py"]},
            {"path": "c.py", "depends_on": ["a.py"]},  # cycle
            {"path": "d.py", "depends_on": ["e.py"]},
            {"path": "e.py", "depends_on": []},
        ],
        "dependencies": [],
        "tests": ["test_a.py"],
    }
    errors = validate_spec(spec)
    cycle_errors = [e for e in errors if e.rule == "no_dependency_cycles"]
    assert len(cycle_errors) > 0
    assert "Cycle detected" in cycle_errors[0].message
    # Cycles are fatal.
    assert any(e.rule == "no_dependency_cycles" for e in fatal_errors(errors))


def test_missing_tests_is_warning_not_fatal():
    spec = {"files": [{"path": "a.py", "exports": ["a"]}], "dependencies": [], "tests": []}
    errors = validate_spec(spec)
    test_errors = [e for e in errors if e.rule == "has_tests"]
    assert len(test_errors) == 1
    assert test_errors[0].severity == "warning"
    assert fatal_errors(errors) == []  # nothing fatal


def test_config_files_not_flagged_for_missing_exports():
    # E4: Dockerfile / .env / pyproject.toml legitimately have no exports.
    spec = {
        "files": [
            {"path": "Dockerfile", "role": "infra"},
            {"path": ".env", "role": "config"},
            {"path": "pyproject.toml"},
            {"path": "README.md"},
        ],
        "tests": [{"path": "tests/test_x.py"}],
    }
    errors = validate_spec(spec)
    assert [e for e in errors if e.rule == "has_exports"] == []


def test_external_dependencies_not_flagged_as_missing_imports():
    # E4: external packages (fastapi, zod) must not be treated as local files.
    spec = {
        "files": [
            {"path": "app/main.py", "depends_on": ["fastapi", "app/config.py"], "exports": ["app"]},
            {"path": "app/config.py", "exports": ["Settings"]},
        ],
        "tests": [{"path": "tests/test_main.py"}],
    }
    errors = validate_spec(spec)
    import_errors = [e for e in errors if e.rule == "imports_resolve"]
    # fastapi (external) is fine; app/config.py exists -> no errors at all.
    assert import_errors == []


def test_missing_local_dependency_is_warning():
    spec = {
        "files": [
            {"path": "app/main.py", "depends_on": ["app/missing.py"], "exports": ["app"]},
        ],
        "tests": [{"path": "tests/test_main.py"}],
    }
    errors = validate_spec(spec)
    import_errors = [e for e in errors if e.rule == "imports_resolve"]
    assert len(import_errors) == 1
    assert import_errors[0].severity == "warning"


def test_signature_match_uses_public_api():
    spec = {
        "files": [
            {
                "path": "app/svc.py",
                "exports": ["do_thing"],
                "public_api": [
                    {"name": "do_thing", "signature": "def do_thing() -> None"},
                    {"name": "no_sig"},  # missing signature -> warning
                ],
            }
        ],
        "tests": [{"path": "tests/test_svc.py"}],
    }
    errors = validate_spec(spec)
    sig_errors = [e for e in errors if e.rule == "signature_match"]
    assert len(sig_errors) == 1
    assert "no_sig" in sig_errors[0].message
    assert sig_errors[0].severity == "warning"


def test_coverage_matches_parameterised_path():
    manifest = {"endpoints": [{"method": "GET", "path": "/users/{id}"}], "data_models": []}
    spec = {
        "files": [
            {"path": "app/routes/users.py", "role": "route", "exports": ["router"],
             "public_api": [{"name": "get_user", "signature": "GET /users/:id"}]},
        ],
        "tests": [{"path": "tests/test_users.py"}],
    }
    # "users" segment appears in the spec, "{id}" is a param so it's ignored.
    errors = validate_spec(spec, manifest)
    coverage_errors = [e for e in errors if e.rule == "coverage_gate"]
    assert coverage_errors == []


def test_duplicate_paths_fatal():
    spec = {
        "files": [
            {"path": "a.py", "exports": ["a"]},
            {"path": "a.py", "exports": ["a"]},
        ],
        "tests": [{"path": "tests/t.py"}],
    }
    errors = validate_spec(spec)
    assert any(e.rule == "no_duplicate_paths" for e in fatal_errors(errors))

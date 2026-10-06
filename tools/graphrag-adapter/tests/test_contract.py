from __future__ import annotations

import importlib
import json
from pathlib import Path
import tomllib

import pytest

from agent_memory_graphrag.__main__ import COMMANDS, main


def test_project_pins_supported_graphrag_and_python_range() -> None:
    project = Path(__file__).parents[1] / "pyproject.toml"
    text = project.read_text(encoding="utf-8")

    assert 'requires-python = ">=3.11,<3.14"' in text
    assert '"graphrag==3.1.2"' in text
    assert "git+" not in text


def test_lock_freezes_graphrag_to_pypi_artifacts_with_hashes() -> None:
    lock_path = Path(__file__).parents[1] / "uv.lock"
    lock = tomllib.loads(lock_path.read_text(encoding="utf-8"))
    packages = {package["name"]: package for package in lock["package"]}

    graphrag = packages["graphrag"]
    assert graphrag["version"] == "3.1.2"
    assert graphrag["source"] == {"registry": "https://pypi.org/simple"}
    assert graphrag["sdist"]["hash"].startswith("sha256:")
    assert all(wheel["hash"].startswith("sha256:") for wheel in graphrag["wheels"])
    assert all("git" not in package.get("source", {}) for package in lock["package"])


def test_adapter_uses_installed_public_graphrag_api() -> None:
    api = importlib.import_module("graphrag.api")

    assert api.__name__ == "graphrag.api"


def test_cli_exposes_only_owned_indexing_contract(capsys: pytest.CaptureFixture[str]) -> None:
    assert COMMANDS == {
        "readiness",
        "full-index",
        "incremental-update",
        "cancel",
        "inspect-artifacts",
    }

    exit_code = main(["readiness"])
    response = json.loads(capsys.readouterr().out)

    assert exit_code == 0
    assert response["contract_version"] == "graph-adapter/v1"
    assert response["state"] == "ready"
    assert response["graphrag_version"] == "3.1.2"


@pytest.mark.parametrize("command", sorted(COMMANDS - {"readiness", "cancel"}))
def test_commands_require_owned_request_manifest(command: str, capsys: pytest.CaptureFixture[str]) -> None:
    exit_code = main([command])
    response = json.loads(capsys.readouterr().out)

    assert exit_code == 2
    assert response["contract_version"] == "graph-adapter/v1"
    assert response["state"] == "failed"
    assert response["reason_code"] == "request_manifest_required"


def test_cancel_is_bounded_and_content_free(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["cancel"]) == 0
    assert json.loads(capsys.readouterr().out) == {"contract_version": "graph-adapter/v1", "state": "cancelled"}

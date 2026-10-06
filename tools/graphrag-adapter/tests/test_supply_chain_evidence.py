import importlib.util
import json
from pathlib import Path

import pytest


SCRIPT = Path(__file__).parents[1] / "scripts" / "supply_chain_evidence.py"
SPEC = importlib.util.spec_from_file_location("supply_chain_evidence", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def artifact_set(tmp_path: Path, severity: str | None = None) -> dict[str, Path]:
    digest = "sha256:" + "a" * 64
    sbom = {
        "bomFormat": "CycloneDX",
        "specVersion": "1.6",
        "components": [
            {"name": "graphrag", "version": "3.1.2", "licenses": [{"license": {"id": "MIT"}}]},
        ],
    }
    licenses = MODULE.build_license_inventory(json.dumps(sbom).encode("utf-8"))
    values = {
        "lock": 'name = "graphrag"\nversion = "3.1.2"\n',
        "requirements": "graphrag==3.1.2 --hash=sha256:" + "b" * 64 + "\n",
        "sbom": json.dumps(sbom),
        "licenses": json.dumps(licenses),
        "license_policy": json.dumps({
            "schema": "agent-memory-graphrag-license-policy/v1",
            "policy_id": "test-production-v1",
            "prohibited_identifiers": ["AGPL-3.0-only", "SSPL-1.0"],
        }),
        "vulnerabilities": json.dumps({"matches": [] if severity is None else [{"vulnerability": {"severity": severity}}]}),
        "signature": json.dumps([{"critical": {"identity": {"docker-reference": "ghcr.io/example/adapter"}, "image": {"docker-manifest-digest": digest}, "type": "cosign container image signature"}}]),
    }
    result = {}
    for name, content in values.items():
        path = tmp_path / f"{name}.json"
        path.write_text(content, encoding="utf-8")
        result[name] = path
    return result


def report_input(tmp_path: Path, severity: str | None = None):
    artifacts = artifact_set(tmp_path, severity)
    digest = "sha256:" + "a" * 64
    return MODULE.SupplyChainInput(
        release_commit="c" * 40,
        image=f"ghcr.io/example/adapter@{digest}",
        graphrag_version="3.1.2",
        adapter_version="0.1.0",
        python_version="3.13.14",
        generated_at="2026-08-27T00:00:00Z",
        lock=artifacts["lock"],
        requirements=artifacts["requirements"],
        sbom=artifacts["sbom"],
        licenses=artifacts["licenses"],
        license_policy=artifacts["license_policy"],
        vulnerabilities=artifacts["vulnerabilities"],
        signature_verification=artifacts["signature"],
    )


def test_builds_digest_bound_complete_supply_chain_report(tmp_path: Path):
    report = MODULE.build_report(report_input(tmp_path))

    assert report["schema"] == "agent-memory-graphrag-supply-chain/v1"
    assert report["image"].endswith("@sha256:" + "a" * 64)
    assert report["vulnerability_policy"] == {"threshold": "high", "blocking_findings": 0}
    assert report["signature_verified"] is True
    assert report["license_policy"] == {"policy_id": "test-production-v1", "prohibited_matches": 0}
    assert set(report["artifacts"]) == {"lock", "requirements", "sbom", "licenses", "license_policy", "vulnerabilities", "signature_verification"}
    assert all(value.startswith("sha256:") and len(value) == 71 for value in report["artifacts"].values())


@pytest.mark.parametrize("severity", ["High", "Critical"])
def test_rejects_release_blocking_vulnerability(tmp_path: Path, severity: str):
    with pytest.raises(ValueError, match="release-blocking vulnerability"):
        MODULE.build_report(report_input(tmp_path, severity))


def test_rejects_signature_for_another_digest(tmp_path: Path):
    replacement_case = tmp_path / "replacement-case"
    replacement_case.mkdir()
    input_value = report_input(replacement_case)
    input_value.signature_verification.write_text(
        json.dumps([{"critical": {"identity": {"docker-reference": "ghcr.io/example/adapter"}, "image": {"docker-manifest-digest": "sha256:" + "d" * 64}, "type": "cosign container image signature"}}]),
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="signature verification does not bind"):
        MODULE.build_report(input_value)


def test_rejects_non_cyclonedx_sbom_and_empty_license_inventory(tmp_path: Path):
    input_value = report_input(tmp_path)
    input_value.sbom.write_text(json.dumps({"bomFormat": "SPDX"}), encoding="utf-8")
    with pytest.raises(ValueError, match="CycloneDX"):
        MODULE.build_report(input_value)

    input_value = report_input(tmp_path)
    input_value.licenses.write_text("\n", encoding="utf-8")
    with pytest.raises(ValueError, match="license inventory"):
        MODULE.build_report(input_value)


def test_builds_deterministic_sbom_bound_license_inventory():
    sbom = json.dumps({
        "bomFormat": "CycloneDX",
        "components": [
            {"name": "zeta", "version": "2", "purl": "pkg:pypi/zeta@2"},
            {
                "name": "alpha",
                "version": "1",
                "licenses": [
                    {"license": {"name": "MIT License"}},
                    {"expression": "Apache-2.0 OR MIT"},
                ],
            },
        ],
    }).encode("utf-8")

    inventory = MODULE.build_license_inventory(sbom)

    assert inventory["schema"] == "agent-memory-graphrag-license-inventory/v1"
    assert inventory["sbom_sha256"].startswith("sha256:")
    assert inventory["unknown_license_components"] == 1
    assert inventory["components"] == [
        {"licenses": ["Apache-2.0 OR MIT", "MIT License"], "name": "alpha", "purl": "", "version": "1"},
        {"licenses": ["NOASSERTION"], "name": "zeta", "purl": "pkg:pypi/zeta@2", "version": "2"},
    ]


def test_rejects_license_inventory_not_bound_to_sbom(tmp_path: Path):
    input_value = report_input(tmp_path)
    inventory = json.loads(input_value.licenses.read_text(encoding="utf-8"))
    inventory["sbom_sha256"] = "sha256:" + "f" * 64
    input_value.licenses.write_text(json.dumps(inventory), encoding="utf-8")

    with pytest.raises(ValueError, match="does not bind the SBOM"):
        MODULE.build_report(input_value)


def test_rejects_prohibited_license_expression(tmp_path: Path):
    input_value = report_input(tmp_path)
    inventory = json.loads(input_value.licenses.read_text(encoding="utf-8"))
    inventory["components"][0]["licenses"] = ["MIT OR AGPL-3.0-only"]
    input_value.licenses.write_text(json.dumps(inventory), encoding="utf-8")

    with pytest.raises(ValueError, match="prohibited license"):
        MODULE.build_report(input_value)


def test_rejects_symlink_and_post_open_artifact_replacement(tmp_path: Path):
    input_value = report_input(tmp_path)
    real_license = tmp_path / "real-licenses.txt"
    real_license.write_text("MIT\n", encoding="utf-8")
    input_value.licenses.unlink()
    input_value.licenses.symlink_to(real_license)
    with pytest.raises(ValueError, match="regular non-symlink"):
        MODULE.build_report(input_value)

    replacement_case = tmp_path / "replacement-case"
    replacement_case.mkdir()
    input_value = report_input(replacement_case)

    def replace_lock(name: str, path: Path):
        if name == "lock":
            path.rename(replacement_case / "opened-lock.json")
            path.write_text("replacement", encoding="utf-8")

    with pytest.raises(ValueError, match="changed during validation"):
        MODULE.build_report(input_value, after_open=replace_lock)


def test_report_publication_is_mode_0600_create_only_and_parent_anchored(tmp_path: Path):
    report = MODULE.build_report(report_input(tmp_path))
    destination = tmp_path / "evidence" / "report.json"
    destination.parent.mkdir()
    MODULE.publish_report(destination, report)
    assert destination.stat().st_mode & 0o777 == 0o600
    with pytest.raises(FileExistsError):
        MODULE.publish_report(destination, report)

    original = tmp_path / "original"
    replacement = tmp_path / "replacement"
    parent = tmp_path / "mutable"
    parent.mkdir()

    def replace_parent():
        parent.rename(original)
        replacement.mkdir()
        replacement.rename(parent)

    with pytest.raises(ValueError, match="parent directory changed"):
        MODULE.publish_report(parent / "report.json", report, after_parent_open=replace_parent)
    assert not (original / "report.json").exists()
    assert not (parent / "report.json").exists()

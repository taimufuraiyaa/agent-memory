#!/usr/bin/env python3
"""Create a content-free, digest-bound GraphRAG supply-chain report."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import stat
import sys
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import NamedTuple


MAXIMUM_ARTIFACT_BYTES = 64 * 1024 * 1024
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
VERSION = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")


class SupplyChainInput(NamedTuple):
    release_commit: str
    image: str
    graphrag_version: str
    adapter_version: str
    python_version: str
    generated_at: str
    lock: Path
    requirements: Path
    sbom: Path
    licenses: Path
    license_policy: Path
    vulnerabilities: Path
    signature_verification: Path


class OpenedArtifact(NamedTuple):
    name: str
    path: Path
    descriptor: int
    validated: os.stat_result
    opened: os.stat_result
    content: bytes


def _same_file(left: os.stat_result, right: os.stat_result) -> bool:
    return os.path.samestat(left, right)


def _unchanged(left: os.stat_result, right: os.stat_result) -> bool:
    return (
        _same_file(left, right)
        and left.st_size == right.st_size
        and left.st_mtime_ns == right.st_mtime_ns
        and stat.S_ISREG(right.st_mode)
    )


def _open_artifact(name: str, path: Path) -> OpenedArtifact:
    try:
        validated = path.lstat()
    except OSError as error:
        raise ValueError(f"{name} artifact is unavailable") from error
    if not stat.S_ISREG(validated.st_mode) or validated.st_size < 1 or validated.st_size > MAXIMUM_ARTIFACT_BYTES:
        raise ValueError(f"{name} artifact must be a bounded regular non-symlink file")
    flags = os.O_RDONLY | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise ValueError(f"{name} artifact must be a bounded regular non-symlink file") from error
    try:
        opened = os.fstat(descriptor)
        if not _unchanged(validated, opened):
            raise ValueError(f"{name} artifact changed during validation")
        chunks, remaining = [], opened.st_size
        while remaining:
            chunk = os.read(descriptor, min(1024 * 1024, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        content = b"".join(chunks)
        if len(content) != opened.st_size:
            raise ValueError(f"{name} artifact changed during validation")
        return OpenedArtifact(name, path, descriptor, validated, opened, content)
    except Exception:
        os.close(descriptor)
        raise


def _load_artifacts(paths: Mapping[str, Path], after_open: Callable[[str, Path], None] | None = None) -> dict[str, bytes]:
    opened: list[OpenedArtifact] = []
    try:
        for name, path in paths.items():
            artifact = _open_artifact(name, path)
            opened.append(artifact)
            if after_open is not None:
                after_open(name, path)
        for artifact in opened:
            descriptor_state = os.fstat(artifact.descriptor)
            try:
                path_state = artifact.path.lstat()
            except OSError as error:
                raise ValueError(f"{artifact.name} artifact changed during validation") from error
            if not _unchanged(artifact.opened, descriptor_state) or not _unchanged(artifact.opened, path_state):
                raise ValueError(f"{artifact.name} artifact changed during validation")
        return {artifact.name: artifact.content for artifact in opened}
    finally:
        for artifact in opened:
            os.close(artifact.descriptor)


def _strict_json(content: bytes, label: str):
    def reject_duplicates(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"{label} contains duplicate JSON fields")
            result[key] = value
        return result

    try:
        return json.loads(content, object_pairs_hook=reject_duplicates)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"{label} is not valid JSON") from error


def _sha256(content: bytes) -> str:
    return "sha256:" + hashlib.sha256(content).hexdigest()


def _image_identity(image: str) -> tuple[str, str]:
    if image.count("@") != 1:
        raise ValueError("image must be an immutable digest reference")
    repository, digest = image.split("@", 1)
    if not repository or not DIGEST.fullmatch(digest):
        raise ValueError("image must be an immutable digest reference")
    return repository, digest


def _validate_signature(content: bytes, repository: str, digest: str) -> None:
    records = _strict_json(content, "signature verification")
    if not isinstance(records, list) or not records:
        raise ValueError("signature verification is empty")
    for record in records:
        critical = record.get("critical", {}) if isinstance(record, dict) else {}
        identity = critical.get("identity", {}) if isinstance(critical, dict) else {}
        image = critical.get("image", {}) if isinstance(critical, dict) else {}
        if (
            critical.get("type") == "cosign container image signature"
            and identity.get("docker-reference") == repository
            and image.get("docker-manifest-digest") == digest
        ):
            return
    raise ValueError("signature verification does not bind the released image digest")


def _blocking_vulnerabilities(content: bytes) -> int:
    report = _strict_json(content, "vulnerability report")
    matches = report.get("matches") if isinstance(report, dict) else None
    if not isinstance(matches, list):
        raise ValueError("vulnerability report has an unsupported schema")
    blocking = 0
    for match in matches:
        vulnerability = match.get("vulnerability", {}) if isinstance(match, dict) else {}
        if str(vulnerability.get("severity", "")).lower() in {"high", "critical"}:
            blocking += 1
    return blocking


def _component_licenses(component: dict) -> list[str]:
    values: set[str] = set()
    entries = component.get("licenses", [])
    if not isinstance(entries, list):
        raise ValueError("SBOM component licenses have an unsupported schema")
    for entry in entries:
        if not isinstance(entry, dict):
            raise ValueError("SBOM component licenses have an unsupported schema")
        expression = entry.get("expression")
        license_value = entry.get("license")
        if isinstance(expression, str) and expression.strip():
            values.add(expression.strip())
        elif isinstance(license_value, dict):
            identifier = license_value.get("id") or license_value.get("name")
            if isinstance(identifier, str) and identifier.strip():
                values.add(identifier.strip())
    return sorted(values) or ["NOASSERTION"]


def build_license_inventory(sbom_content: bytes) -> dict:
    """Build a deterministic inventory that is cryptographically bound to an SBOM."""
    sbom = _strict_json(sbom_content, "SBOM")
    if not isinstance(sbom, dict) or sbom.get("bomFormat") != "CycloneDX":
        raise ValueError("SBOM must use CycloneDX")
    raw_components = sbom.get("components")
    if not isinstance(raw_components, list) or not raw_components:
        raise ValueError("SBOM must contain components")
    components = []
    unknown = 0
    for component in raw_components:
        if not isinstance(component, dict):
            raise ValueError("SBOM components have an unsupported schema")
        name, version, purl = component.get("name"), component.get("version", ""), component.get("purl", "")
        if not isinstance(name, str) or not name.strip():
            raise ValueError("SBOM component name is missing")
        if not isinstance(version, str) or not isinstance(purl, str):
            raise ValueError("SBOM component identity has an unsupported schema")
        licenses = _component_licenses(component)
        unknown += int(licenses == ["NOASSERTION"])
        components.append({
            "licenses": licenses,
            "name": name.strip(),
            "purl": purl.strip(),
            "version": version.strip(),
        })
    components.sort(key=lambda item: (item["name"], item["version"], item["purl"], item["licenses"]))
    return {
        "schema": "agent-memory-graphrag-license-inventory/v1",
        "sbom_sha256": _sha256(sbom_content),
        "unknown_license_components": unknown,
        "components": components,
    }


def _validate_license_inventory(content: bytes, sbom_content: bytes) -> dict:
    if not content.strip():
        raise ValueError("license inventory is empty")
    inventory = _strict_json(content, "license inventory")
    if not isinstance(inventory, dict) or inventory.get("schema") != "agent-memory-graphrag-license-inventory/v1":
        raise ValueError("license inventory has an unsupported schema")
    if inventory.get("sbom_sha256") != _sha256(sbom_content):
        raise ValueError("license inventory does not bind the SBOM")
    if not isinstance(inventory.get("components"), list) or not inventory["components"]:
        raise ValueError("license inventory is empty")
    return inventory


def _validate_license_policy(content: bytes, inventory: dict) -> dict:
    policy = _strict_json(content, "license policy")
    if not isinstance(policy, dict) or policy.get("schema") != "agent-memory-graphrag-license-policy/v1":
        raise ValueError("license policy has an unsupported schema")
    policy_id = policy.get("policy_id")
    prohibited = policy.get("prohibited_identifiers")
    if not isinstance(policy_id, str) or not policy_id.strip():
        raise ValueError("license policy identifier is missing")
    if not isinstance(prohibited, list) or not prohibited:
        raise ValueError("license policy prohibited identifiers are empty")
    normalized: set[str] = set()
    for identifier in prohibited:
        if not isinstance(identifier, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.+-]{1,63}", identifier):
            raise ValueError("license policy contains an invalid prohibited identifier")
        lowered = identifier.lower()
        if lowered in normalized:
            raise ValueError("license policy contains duplicate prohibited identifiers")
        normalized.add(lowered)
    matches = []
    for component in inventory["components"]:
        for expression in component.get("licenses", []):
            tokens = {token.lower() for token in re.findall(r"[A-Za-z0-9][A-Za-z0-9.+-]*", expression)}
            found = sorted(tokens & normalized)
            if found:
                matches.append({"component": component.get("name", ""), "identifiers": found})
    if matches:
        raise ValueError("prohibited license is present")
    return {"policy_id": policy_id.strip(), "prohibited_matches": 0}


def build_report(value: SupplyChainInput, after_open: Callable[[str, Path], None] | None = None) -> dict:
    if not COMMIT.fullmatch(value.release_commit):
        raise ValueError("release commit must be a full Git SHA")
    if not VERSION.fullmatch(value.graphrag_version) or not VERSION.fullmatch(value.adapter_version) or not VERSION.fullmatch(value.python_version):
        raise ValueError("dependency versions must be exact semantic versions")
    repository, digest = _image_identity(value.image)
    paths = {
        "lock": value.lock,
        "requirements": value.requirements,
        "sbom": value.sbom,
        "licenses": value.licenses,
        "license_policy": value.license_policy,
        "vulnerabilities": value.vulnerabilities,
        "signature_verification": value.signature_verification,
    }
    content = _load_artifacts(paths, after_open)
    lock_text = content["lock"].decode("utf-8", errors="strict")
    requirements_text = content["requirements"].decode("utf-8", errors="strict")
    if f'name = "graphrag"' not in lock_text or f'version = "{value.graphrag_version}"' not in lock_text:
        raise ValueError("lock does not bind the GraphRAG version")
    if f"graphrag=={value.graphrag_version}" not in requirements_text or "--hash=sha256:" not in requirements_text:
        raise ValueError("requirements do not bind a hashed GraphRAG package")
    sbom = _strict_json(content["sbom"], "SBOM")
    if not isinstance(sbom, dict) or sbom.get("bomFormat") != "CycloneDX":
        raise ValueError("SBOM must use CycloneDX")
    license_inventory = _validate_license_inventory(content["licenses"], content["sbom"])
    license_policy = _validate_license_policy(content["license_policy"], license_inventory)
    blocking = _blocking_vulnerabilities(content["vulnerabilities"])
    if blocking:
        raise ValueError("release-blocking vulnerability is present")
    _validate_signature(content["signature_verification"], repository, digest)
    return {
        "schema": "agent-memory-graphrag-supply-chain/v1",
        "release_commit": value.release_commit,
        "image": value.image,
        "graphrag_version": value.graphrag_version,
        "adapter_version": value.adapter_version,
        "python_version": value.python_version,
        "generated_at": value.generated_at,
        "vulnerability_policy": {"threshold": "high", "blocking_findings": 0},
        "license_inventory": {
            "components": len(license_inventory["components"]),
            "unknown_license_components": license_inventory.get("unknown_license_components", 0),
        },
        "license_policy": license_policy,
        "signature_verified": True,
        "artifacts": {name: _sha256(data) for name, data in content.items()},
    }


def _same_directory(left: os.stat_result, right: os.stat_result) -> bool:
    return _same_file(left, right) and stat.S_ISDIR(right.st_mode)


def publish_report(
    destination: Path,
    report: dict,
    after_parent_open: Callable[[], None] | None = None,
    after_write: Callable[[], None] | None = None,
) -> None:
    parent, name = destination.parent, destination.name
    if not name or name in {".", ".."} or Path(name).name != name:
        raise ValueError("report destination is invalid")
    validated = parent.lstat()
    if not stat.S_ISDIR(validated.st_mode):
        raise ValueError("report parent must be a regular directory")
    root_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
    directory = os.open(parent, root_flags)
    created = False
    try:
        opened = os.fstat(directory)
        if not _same_directory(validated, opened):
            raise ValueError("report parent directory changed")
        if after_parent_open is not None:
            after_parent_open()
        if not _same_directory(opened, parent.lstat()):
            raise ValueError("report parent directory changed")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(name, flags, 0o600, dir_fd=directory)
        created = True
        try:
            payload = json.dumps(report, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8") + b"\n"
            view = memoryview(payload)
            while view:
                written = os.write(descriptor, view)
                view = view[written:]
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        if after_write is not None:
            after_write()
        if not _same_directory(opened, parent.lstat()):
            raise ValueError("report parent directory changed")
        os.fsync(directory)
        if not _same_directory(opened, parent.lstat()):
            raise ValueError("report parent directory changed")
    except Exception:
        if created:
            try:
                os.unlink(name, dir_fd=directory)
            except OSError:
                pass
        raise
    finally:
        os.close(directory)


def _arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    licenses = commands.add_parser("licenses")
    licenses.add_argument("--sbom", type=Path, required=True)
    licenses.add_argument("--output", type=Path, required=True)
    report = commands.add_parser("report")
    report.add_argument("--release-commit", required=True)
    report.add_argument("--image", required=True)
    report.add_argument("--graphrag-version", default="3.1.2")
    report.add_argument("--adapter-version", default="0.1.0")
    report.add_argument("--python-version", default="3.13.14")
    report.add_argument("--generated-at", required=True)
    report.add_argument("--lock", type=Path, required=True)
    report.add_argument("--requirements", type=Path, required=True)
    report.add_argument("--sbom", type=Path, required=True)
    report.add_argument("--licenses", type=Path, required=True)
    report.add_argument("--license-policy", type=Path, required=True)
    report.add_argument("--vulnerabilities", type=Path, required=True)
    report.add_argument("--signature-verification", type=Path, required=True)
    report.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


def main() -> int:
    args = _arguments()
    try:
        if args.command == "licenses":
            content = _load_artifacts({"sbom": args.sbom})["sbom"]
            publish_report(args.output, build_license_inventory(content))
            return 0
        report = build_report(SupplyChainInput(
            release_commit=args.release_commit,
            image=args.image,
            graphrag_version=args.graphrag_version,
            adapter_version=args.adapter_version,
            python_version=args.python_version,
            generated_at=args.generated_at,
            lock=args.lock,
            requirements=args.requirements,
            sbom=args.sbom,
            licenses=args.licenses,
            license_policy=args.license_policy,
            vulnerabilities=args.vulnerabilities,
            signature_verification=args.signature_verification,
        ))
        publish_report(args.output, report)
    except (OSError, UnicodeError, ValueError, json.JSONDecodeError) as error:
        print(f"supply-chain evidence rejected: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

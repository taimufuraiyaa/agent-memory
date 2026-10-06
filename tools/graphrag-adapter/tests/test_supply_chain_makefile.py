from pathlib import Path


MAKEFILE = (Path(__file__).parents[1] / "Makefile").read_text(encoding="utf-8")


def test_local_supply_chain_target_uses_the_same_digest_bound_evidence_contract():
    required = [
        'case "$(RELEASE_IMAGE)" in *@sha256:*)',
        'syft "$(RELEASE_IMAGE)" -o cyclonedx-json=sbom.cdx.json',
        'grype "$(RELEASE_IMAGE)" --fail-on high --output json',
        "supply_chain_evidence.py licenses",
        "supply_chain_evidence.py report",
        "signature-verification.json",
        "license-policy.json",
        "supply-chain-report.json",
        "supply-chain-report.bundle.json",
        "cosign sign-blob",
    ]
    for value in required:
        assert value in MAKEFILE


def test_local_supply_chain_verifies_image_signature_before_emitting_report():
    verify = MAKEFILE.index("cosign verify --output json")
    report = MAKEFILE.index("supply_chain_evidence.py report")
    sign_report = MAKEFILE.index("cosign sign-blob")
    assert verify < report < sign_report

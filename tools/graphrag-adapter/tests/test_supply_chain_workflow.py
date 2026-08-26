from pathlib import Path


ROOT = Path(__file__).parents[3]
WORKFLOW = (ROOT / ".github/workflows/graphrag-adapter.yml").read_text(encoding="utf-8")


def test_tag_release_publishes_complete_signed_supply_chain_evidence():
    required = [
        "supply_chain_evidence.py",
        "sbom.cdx.json",
        "licenses.txt",
        "license-policy.json",
        "vulnerabilities.json",
        "signature-verification.json",
        "supply-chain-report.json",
        "supply-chain-report.bundle.json",
        "cosign sign-blob",
        "actions/upload-artifact@",
        "if-no-files-found: error",
        "retention-days: 90",
    ]
    for value in required:
        assert value in WORKFLOW
    assert "grype \"$release_image\" --fail-on high --output json" in WORKFLOW
    assert "cosign verify --output json \"$release_image\"" in WORKFLOW
    assert '--certificate-identity "$CERTIFICATE_IDENTITY"' in WORKFLOW
    assert "certificate-identity-regexp" not in WORKFLOW
    assert '--image "$RELEASE_IMAGE"' in WORKFLOW


def test_evidence_generation_occurs_only_after_digest_push_and_signature_verification():
    push = WORKFLOW.index('docker push "$image"')
    resolve = WORKFLOW.index('release_image="$repository@$digest"')
    verify = WORKFLOW.index('cosign verify --output json "$release_image"')
    report = WORKFLOW.index("supply_chain_evidence.py")
    upload = WORKFLOW.index("actions/upload-artifact@")
    assert push < resolve < verify < report < upload

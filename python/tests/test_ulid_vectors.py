"""Replay the committed strict-ULID vectors against the Python implementation.

The vectors live at ``go/strictcli/testdata/ulid_vectors.json`` and are authored in
``conformance/gen_ulid_vectors.py`` -- not derived from any implementation. The
Go and TypeScript suites replay the same file, which is what pins the profile
(docs/process-trace-store.md, "Identifiers") across three independent minters.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import strictcli as sc

VECTORS_PATH = (
    Path(__file__).resolve().parents[2] / "go" / "strictcli" / "testdata" / "ulid_vectors.json"
)
DOC = json.loads(VECTORS_PATH.read_text())


def test_vector_counts_match_the_document():
    assert len(DOC["encode_vectors"]) == DOC["encode_vector_count"]
    assert len(DOC["parse_vectors"]) == DOC["parse_vector_count"]
    assert DOC["encode_vector_count"] > 0
    assert DOC["parse_vector_count"] > 0


@pytest.mark.parametrize(
    "vec", DOC["encode_vectors"], ids=[v["name"] for v in DOC["encode_vectors"]]
)
def test_encode_vector(vec):
    randomness = bytes.fromhex(vec["random_hex"])
    assert len(randomness) == 10
    encoded = sc._ulid_encode(vec["ms"], randomness)
    assert encoded == vec["ulid"]
    assert len(encoded) == 26
    # Every encoding round-trips to the millisecond it carries.
    assert sc._ulid_timestamp(encoded) == vec["ms"]


@pytest.mark.parametrize(
    "vec", DOC["parse_vectors"], ids=[v["name"] for v in DOC["parse_vectors"]]
)
def test_parse_vector(vec):
    if vec["valid"]:
        assert sc._ulid_timestamp(vec["text"]) == vec["ms"]
        assert sc._ulid_valid(vec["text"]) is True
    else:
        assert sc._ulid_timestamp(vec["text"]) is None
        assert sc._ulid_valid(vec["text"]) is False


def test_mint_carries_the_clock_and_is_canonical():
    minted = sc._ulid_mint(1786594672913)
    assert len(minted) == 26
    assert sc._ulid_timestamp(minted) == 1786594672913
    assert all(c in "0123456789ABCDEFGHJKMNPQRSTVWXYZ" for c in minted)


def test_mint_randomness_differs_across_calls():
    # 80 crypto-random bits: a collision in a small sample is not credible.
    minted = {sc._ulid_mint(1786594672913) for _ in range(64)}
    assert len(minted) == 64


def test_parse_rejects_non_strings():
    assert sc._ulid_timestamp(None) is None
    assert sc._ulid_timestamp(12345) is None
    assert sc._ulid_valid(None) is False

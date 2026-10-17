#!/usr/bin/env python3
"""HTTP smoke test with synthetic data. Run only against a test installation."""

import argparse
from datetime import datetime
import json
from pathlib import Path
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


def call(base, method, path, body=None, headers=None, expected=200):
    request_headers = {"Content-Type": "application/json"}
    request_headers.update(headers or {})
    data = None if body is None else json.dumps(body).encode("utf-8")
    request = Request(base + path, data=data, method=method, headers=request_headers)
    try:
        response = urlopen(request, timeout=10)
    except HTTPError as error:
        response = error
    with response:
        assert response.status == expected, f"{method} {path}: HTTP {response.status}"
        if expected == 204:
            assert response.read() == b""
            return None
        assert response.headers.get_content_type() == "application/json"
        result = json.load(response)
        if expected >= 400:
            error = result["error"]
            assert set(error) == {"code", "message", "fields"}
            assert isinstance(error["fields"], dict) and error["message"]
        if expected == 405:
            assert response.headers.get("Allow")
        return result


def wait_ready(base):
    deadline = time.monotonic() + 30
    while True:
        try:
            call(base, "GET", "/api/expenses?limit=1")
            return
        except (URLError, TimeoutError, AssertionError):
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.2)


def check_persistence(base, state):
    page = call(base, "GET", "/api/expenses?" + urlencode({
        "user_id": state["owner"], "sort": "id", "order": "asc",
    }))
    assert page["total"] == len(state["ids"])
    assert [item["id"] for item in page["items"]] == state["ids"]
    for item_id in state["ids"]:
        assert call(base, "GET", "/api/expenses/" + item_id)["id"] == item_id


def member_ids(base):
    response = call(base, "GET", "/api/members")
    assert set(response) == {"items"}
    ids = [member["user_id"] for member in response["items"]]
    assert all(isinstance(user_id, str) for user_id in ids)
    assert ids == sorted(set(ids), key=int)
    return ids


def check_mutations(base, owner):
    original_owner = str(int(owner) + 2)
    new_owner = str(int(owner) + 3)
    item = call(base, "POST", "/api/expenses", {
        "user_id": original_owner, "amount": "350.5", "category": "еда",
        "spent_at": "2026-10-16", "comment": "synthetic",
    }, expected=201)
    path = "/api/expenses/" + item["id"]
    assert original_owner in member_ids(base)
    changed = call(base, "PATCH", path, {"amount": "450"})
    assert changed["amount"] == "450.00"
    assert datetime.fromisoformat(changed["updated_at"]) > datetime.fromisoformat(item["updated_at"])
    for field in ("id", "user_id", "category", "spent_at", "comment", "created_at"):
        assert changed[field] == item[field]
    assert call(base, "GET", path) == changed

    invalid_updates = [
        {}, {"comment": 1}, {"user_id": None}, {"amount": None},
        {"category": None}, {"spent_at": None}, {"id": "1"},
        {"amount": "1.001"}, {"amount": "2", "spent_at": "2026-02-29"},
        {"amount": "2", "unknown": True}, {"created_at": item["created_at"]},
    ]
    for patch in invalid_updates:
        error = call(base, "PATCH", path, patch, expected=400)
        assert error["error"]["code"] == "validation_error"
        assert call(base, "GET", path) == changed

    for comment in (None, "", "synthetic '); DROP TABLE expenses; --"):
        changed = call(base, "PATCH", path, {"comment": comment})
        assert changed["comment"] == comment
        assert changed["amount"] == "450.00"
        assert call(base, "GET", path) == changed
    changed = call(base, "PATCH", path, {
        "user_id": new_owner, "category": "транспорт", "spent_at": "2024-02-29",
    })
    assert changed["user_id"] == new_owner
    assert changed["category"] == "транспорт" and changed["spent_at"] == "2024-02-29"
    assert changed["created_at"] == item["created_at"]
    owners = member_ids(base)
    assert original_owner not in owners and new_owner in owners
    assert call(base, "GET", "/api/expenses?user_id=" + original_owner)["total"] == 0
    assert call(base, "GET", "/api/expenses?user_id=" + new_owner)["items"] == [changed]
    call(base, "PATCH", path, {"amount": "1"}, {"Origin": "https://forbidden.example"}, expected=403)
    call(base, "DELETE", path, headers={"Origin": "https://forbidden.example"}, expected=403)
    call(base, "PATCH", path, {"amount": "1"}, {"Content-Type": "text/plain"}, expected=415)
    assert call(base, "GET", path) == changed
    call(base, "DELETE", path, expected=204)
    call(base, "DELETE", path, expected=404)
    call(base, "GET", path, expected=404)
    call(base, "PATCH", path, {"comment": None}, expected=404)
    assert new_owner not in member_ids(base)
    call(base, "GET", "/api/members?limit=1", expected=400)
    call(base, "POST", "/api/members", {}, expected=405)


def smoke(base):
    owner = str(time.time_ns())
    records = []
    for index, amount in enumerate(["0.10", "2", "10", "0.20"]):
        body = {
            "user_id": owner if index < 3 else str(int(owner) + 1),
            "amount": amount,
            "category": "еда" if index < 2 else "транспорт",
            "spent_at": "2026-10-16" if index != 2 else "2026-10-15",
        }
        if index == 1:
            body["comment"] = ""
        item = call(base, "POST", "/api/expenses", body, expected=201)
        assert set(item) == {"id", "user_id", "amount", "category", "spent_at", "comment", "created_at", "updated_at"}
        assert isinstance(item["id"], str) and item["user_id"] == body["user_id"]
        assert item["amount"] == ["0.10", "2.00", "10.00", "0.20"][index]
        assert item["spent_at"] == body["spent_at"]
        assert item["comment"] == body.get("comment")
        assert item["created_at"].endswith("Z") and item["updated_at"] == item["created_at"]
        assert call(base, "GET", "/api/expenses/" + item["id"]) == item
        records.append(item)

    query = {"user_id": owner, "sort": "amount", "order": "asc", "limit": 1, "offset": 1}
    page = call(base, "GET", "/api/expenses?" + urlencode(query))
    assert page["total"] == 3 and page["limit"] == 1 and page["offset"] == 1
    assert [item["id"] for item in page["items"]] == [records[1]["id"]]
    query["offset"] = 100
    page = call(base, "GET", "/api/expenses?" + urlencode(query))
    assert page["total"] == 3 and page["items"] == []
    query = {"user_id": owner, "category": "еда", "date_from": "2026-10-16", "date_to": "2026-10-16"}
    page = call(base, "GET", "/api/expenses?" + urlencode(query))
    assert page["total"] == 2
    assert [item["id"] for item in page["items"]] == [records[1]["id"], records[0]["id"]]

    valid = {"user_id": owner, "amount": "1", "category": "еда", "spent_at": "2026-10-16"}
    for patch in [{"amount": "0"}, {"amount": 1}, {"amount": "1.001"}, {"user_id": "01"}, {"spent_at": "2026-02-29"}, {"id": "1"}, {"category": None}]:
        error = call(base, "POST", "/api/expenses", valid | patch, expected=400)
        assert error["error"]["code"] == "validation_error"
    for query_string in ["limit=0", "unknown=1", "user_id=1&user_id=2", "category=", "sort=invalid"]:
        assert call(base, "GET", "/api/expenses?" + query_string, expected=400)["error"]["code"] == "validation_error"
    call(base, "GET", "/api/expenses/01", expected=400)
    call(base, "GET", "/api/expenses/9223372036854775807", expected=404)
    call(base, "GET", "/api/missing", expected=404)
    call(base, "PUT", "/api/expenses/" + records[0]["id"], expected=405)
    call(base, "POST", "/api/expenses", valid, {"Content-Type": "text/plain"}, expected=415)
    call(base, "POST", "/api/expenses", valid, {"Origin": "https://forbidden.example"}, expected=403)
    check_mutations(base, owner)
    state = {"owner": owner, "ids": [item["id"] for item in records[:3]]}
    check_persistence(base, state)
    return state


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8080")
    parser.add_argument("--state-file", type=Path)
    parser.add_argument("--verify", action="store_true", help="Only verify records saved before restart")
    args = parser.parse_args()
    wait_ready(args.url)
    if args.verify:
        if args.state_file is None:
            parser.error("--verify requires --state-file")
        check_persistence(args.url, json.loads(args.state_file.read_text()))
        print("Persistence smoke passed.")
    else:
        state = smoke(args.url)
        if args.state_file:
            args.state_file.write_text(json.dumps(state))
        print("HTTP smoke passed: CRUD, partial updates, reassignment, members, filters, sorting, pagination and errors.")


if __name__ == "__main__":
    main()

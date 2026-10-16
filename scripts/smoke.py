#!/usr/bin/env python3
"""HTTP smoke test with synthetic data. Run only against a test installation."""

import argparse
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
    call(base, "DELETE", "/api/expenses/" + records[0]["id"], expected=405)
    call(base, "POST", "/api/expenses", valid, {"Content-Type": "text/plain"}, expected=415)
    call(base, "POST", "/api/expenses", valid, {"Origin": "https://forbidden.example"}, expected=403)
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
        print("HTTP smoke passed: create, get, filters, sorting, pagination and errors.")


if __name__ == "__main__":
    main()

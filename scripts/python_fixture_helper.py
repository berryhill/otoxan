#!/usr/bin/env python3
"""
python_fixture_helper.py — Bridge between Go parity tests and Python MongoDB stores.

Reads/writes documents via the Python store modules and emits JSON over stdout.

Usage:
    python3 python_fixture_helper.py --store <name> --op write|read --id <id> [--db <db>]

--store values: tasks, plans, teams, directives, reports, flows, memory, notifications, auth, queue
--db defaults to "silas" (the canonical test agent DB).

Environment:
    MONGO_URI  — MongoDB connection string (required)
"""

import argparse
import json
import os
import sys
import traceback
from datetime import datetime, timezone

# Ensure the Python store modules are importable
sys.path.insert(0, "/home/silas/.hermes/scripts")
sys.path.insert(0, "/home/silas/.hermes/profiles/default/skills")


def _json_default(obj):
    """Serialize datetime and ObjectId for JSON."""
    if isinstance(obj, datetime):
        return obj.isoformat()
    # ObjectId is optional; handle gracefully
    try:
        from bson.objectid import ObjectId
        if isinstance(obj, ObjectId):
            return str(obj)
    except Exception:
        pass
    raise TypeError(f"Object of type {type(obj).__name__} is not JSON serializable")


def _get_mongo_uri():
    uri = os.environ.get("MONGO_URI")
    if not uri:
        print(json.dumps({"error": "MONGO_URI not set"}), file=sys.stderr)
        sys.exit(1)
    return uri


def _connect():
    """Return a raw pymongo client using MONGO_URI."""
    from pymongo import MongoClient
    uri = _get_mongo_uri()
    # Testcontainers MongoDB has no auth by default; avoid authSource issues
    return MongoClient(uri, connectTimeoutMS=5000, serverSelectionTimeoutMS=5000)


# ------------------------------------------------------------------
# Store dispatch
# ------------------------------------------------------------------

_STORE_CONFIG = {
    "tasks":      {"db": "silas", "collection": "tasks",      "id_field": "task_id"},
    "plans":      {"db": "silas", "collection": "plans",      "id_field": "plan_id"},
    "teams":      {"db": "global", "collection": "teams",     "id_field": "team_id"},
    "directives": {"db": "silas", "collection": "directives", "id_field": "directive_id"},
    "reports":    {"db": "silas", "collection": "reports",    "id_field": "report_id"},
    "flows":      {"db": "silas", "collection": "flows",      "id_field": "flow_id"},
    "memory":     {"db": "silas", "collection": "memory",     "id_field": "memory_id"},
    "notifications": {"db": "silas", "collection": "notifications", "id_field": "notification_id"},
    "auth":       {"db": "silas", "collection": "auth",       "id_field": "user_id"},
    "queue":      {"db": "silas", "collection": "tasks",      "id_field": "task_id"},
}


def _collection(store_name, db_override=None):
    cfg = _STORE_CONFIG.get(store_name)
    if not cfg:
        raise ValueError(f"Unknown store: {store_name}")
    db_name = db_override or cfg["db"]
    client = _connect()
    return client[db_name][cfg["collection"]], client, cfg["id_field"]


def _strip_mongo_id(doc):
    """Remove MongoDB _id so Go/Python can compare by business key."""
    if doc is None:
        return None
    d = dict(doc)
    d.pop("_id", None)
    return d


def _read_via_store(store_name, doc_id, db_override=None):
    """Read a document using the Python store module (if available) or raw collection."""
    # Prefer store module when available for exact semantics
    store_modules = {
        "tasks": "taskstore",
        "plans": "planstore",
        "teams": "teamstore",
        "directives": "directivestore",
        "reports": "reportstore",
        "flows": "flowstore",
        "memory": "agent_memory",
        "notifications": "notifications",
        "queue": "taskqueue",
    }
    mod_name = store_modules.get(store_name)
    if mod_name:
        try:
            mod = __import__(mod_name)
            store_cls = getattr(mod, mod_name.replace("_", "").title().replace("store", "Store").replace("memory", "Memory").replace("notifications", "Notification").replace("queue", "TaskQueue"), None)
            # Fallback: try exact class names
            if store_cls is None:
                exact_names = {
                    "taskstore": "TaskStore",
                    "planstore": "PlanStore",
                    "teamstore": "TeamStore",
                    "directivestore": "DirectiveStore",
                    "reportstore": "ReportStore",
                    "flowstore": "FlowStore",
                    "agent_memory": "AgentMemory",
                    "notifications": "NotificationStore",
                    "taskqueue": "TaskQueue",
                }
                store_cls = getattr(mod, exact_names.get(mod_name, ""), None)
            if store_cls:
                # Some stores take agent name; some take no args
                if store_name in ("teams",):
                    store = store_cls()
                else:
                    store = store_cls("silas")
                # Map store_name to getter method
                getter_map = {
                    "tasks": "get_task",
                    "plans": "get",
                    "teams": "get_team",
                    "directives": "get",
                    "reports": "get",
                    "flows": "get",
                    "memory": "get",
                    "notifications": "get",
                    "queue": "get_task",
                }
                getter = getter_map.get(store_name, "get")
                fn = getattr(store, getter, None)
                if fn:
                    doc = fn(doc_id)
                    if doc:
                        return _strip_mongo_id(doc)
        except Exception:
            # Fall through to raw collection read
            pass

    # Raw collection fallback
    coll, client, id_field = _collection(store_name, db_override)
    try:
        doc = coll.find_one({id_field: doc_id})
        return _strip_mongo_id(doc)
    finally:
        client.close()


def _write_via_store(store_name, doc_id, db_override=None):
    """Write a minimal fixture document using the Python store module or raw insert."""
    store_modules = {
        "tasks": "taskstore",
        "plans": "planstore",
        "teams": "teamstore",
        "directives": "directivestore",
        "reports": "reportstore",
        "flows": "flowstore",
        "memory": "agent_memory",
        "notifications": "notifications",
        "queue": "taskqueue",
    }
    now = datetime.now(timezone.utc)
    mod_name = store_modules.get(store_name)
    if mod_name:
        try:
            mod = __import__(mod_name)
            exact_names = {
                "taskstore": "TaskStore",
                "planstore": "PlanStore",
                "teamstore": "TeamStore",
                "directivestore": "DirectiveStore",
                "reportstore": "ReportStore",
                "flowstore": "FlowStore",
                "agent_memory": "AgentMemory",
                "notifications": "NotificationStore",
                "taskqueue": "TaskQueue",
            }
            store_cls = getattr(mod, exact_names.get(mod_name, ""), None)
            if store_cls:
                if store_name in ("teams",):
                    store = store_cls()
                else:
                    store = store_cls("silas")

                # Minimal fixture payloads that mirror Go model defaults
                if store_name == "tasks":
                    fn = getattr(store, "create_task", None)
                    if fn:
                        fn(doc_id, title="Parity fixture", status="QUEUED", priority=2)
                        return _strip_mongo_id(store.get_task(doc_id))
                elif store_name == "plans":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, title="Parity fixture", content="", tags=[], plan_type="standard")
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "teams":
                    fn = getattr(store, "create_team", None)
                    if fn:
                        fn(doc_id, "Parity fixture", members=[])
                        return _strip_mongo_id(store.get_team(doc_id))
                elif store_name == "directives":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, title="Parity fixture", content="", category="general", priority=5)
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "reports":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, title="Parity fixture", content="", tags=[])
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "flows":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, name="Parity fixture", description="", steps=[], tags=[])
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "memory":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, agent_id="silas", type="observation", content="Parity fixture", tags=[])
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "notifications":
                    fn = getattr(store, "create", None)
                    if fn:
                        fn(doc_id, recipient_id="silas", channel="in_app", title="Parity fixture", body="")
                        return _strip_mongo_id(store.get(doc_id))
                elif store_name == "queue":
                    fn = getattr(store, "create_task", None)
                    if fn:
                        fn(doc_id, title="Parity fixture", status="QUEUED", priority=2)
                        return _strip_mongo_id(store.get_task(doc_id))
        except Exception:
            pass

    # Raw collection fallback — insert minimal document
    coll, client, id_field = _collection(store_name, db_override)
    try:
        # Hard-delete any existing doc with same id so test is idempotent
        try:
            coll.delete_one({id_field: doc_id})
        except Exception:
            pass
        payload = {id_field: doc_id, "created_at": now, "updated_at": now}
        # Add store-specific required fields
        if store_name == "tasks":
            payload.update({"title": "Parity fixture", "status": "QUEUED", "type": "internal", "priority": 2, "assignee": "silas", "assignee_type": "agent", "max_retries": 3, "attempts": 0, "labels": [], "depends_on": [], "artifacts": [], "flow_delegated_sessions": [], "retry_config": {"backoff": "exponential", "initial_delay_seconds": 30, "max_delay_seconds": 300, "multiplier": 2}, "failure_context": {"notify_channel": "", "include_logs": True, "include_summary": True}, "failure_pattern": "notify_and_halt", "intent": "", "implementation": "", "references": "", "plan_goal": "", "plan_context": "", "phase_context": "", "scheduled_reason": ""})
        elif store_name == "plans":
            payload.update({"title": "Parity fixture", "status": "PLANNING", "owner": "silas", "content": "", "tags": [], "created_session": "", "updated_sessions": [], "plan_type": "standard"})
        elif store_name == "teams":
            payload.update({"name": "Parity fixture", "db_name": "silas", "members": [], "artifacts": {}, "status": "FORMING"})
        elif store_name == "directives":
            payload.update({"title": "Parity fixture", "content": "", "category": "general", "priority": 5, "enabled": True, "tags": [], "owner": "silas"})
        elif store_name == "reports":
            payload.update({"title": "Parity fixture", "status": "DRAFT", "owner": "silas", "content": "", "tags": [], "created_session": "", "updated_sessions": []})
        elif store_name == "flows":
            payload.update({"name": "Parity fixture", "description": "", "status": "DRAFT", "version": 1, "steps": [], "tags": []})
        elif store_name == "memory":
            payload.update({"agent_id": "silas", "type": "observation", "content": "Parity fixture", "tags": [], "importance": 0.5})
        elif store_name == "notifications":
            payload.update({"recipient_id": "silas", "channel": "in_app", "status": "PENDING", "title": "Parity fixture", "body": "", "payload": {}})
        elif store_name == "auth":
            payload.update({"username": doc_id, "email": f"{doc_id}@test.com", "role": "user", "active": True})
        elif store_name == "queue":
            payload.update({"title": "Parity fixture", "status": "QUEUED", "type": "internal", "priority": 2, "assignee": "silas", "assignee_type": "agent", "max_retries": 3, "attempts": 0, "labels": [], "depends_on": [], "artifacts": [], "flow_delegated_sessions": [], "retry_config": {"backoff": "exponential", "initial_delay_seconds": 30, "max_delay_seconds": 300, "multiplier": 2}, "failure_context": {"notify_channel": "", "include_logs": True, "include_summary": True}, "failure_pattern": "notify_and_halt", "intent": "", "implementation": "", "references": "", "plan_goal": "", "plan_context": "", "phase_context": "", "scheduled_reason": ""})

        coll.insert_one(payload)
        doc = coll.find_one({id_field: doc_id})
        return _strip_mongo_id(doc)
    finally:
        client.close()


def main():
    parser = argparse.ArgumentParser(description="Python fixture helper for Go parity tests")
    parser.add_argument("--store", required=True, help="Store name")
    parser.add_argument("--op", required=True, choices=["write", "read"], help="Operation")
    parser.add_argument("--id", required=True, help="Document business ID")
    parser.add_argument("--db", default=None, help="Override database name")
    args = parser.parse_args()

    try:
        if args.op == "write":
            result = _write_via_store(args.store, args.id, args.db)
        else:
            result = _read_via_store(args.store, args.id, args.db)

        if result is None:
            # Emit empty JSON object so Go side knows "not found"
            print(json.dumps({"found": False}))
            sys.exit(0)

        result["found"] = True
        print(json.dumps(result, default=_json_default))
    except Exception as e:
        err = {"error": str(e), "traceback": traceback.format_exc()}
        print(json.dumps(err), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()

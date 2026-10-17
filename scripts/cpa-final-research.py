#!/usr/bin/env python3
"""Explicit CPA-only research driver; private output, bounded spend and progress."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import time


def atomic(path, value):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(path)


def runtime():
    raw = subprocess.check_output(["docker", "inspect", "cliproxyapi-setup-cli-proxy-api-1"])
    container = json.loads(raw)[0]
    config = Path("/home/kirill/p/CLIProxyAPI-setup/state/cpa/config.yaml")
    return {"image": container["Config"]["Image"], "imageID": container["Image"],
            "startedAt": container["State"]["StartedAt"], "restarts": container["RestartCount"],
            "running": container["State"]["Running"],
            "configSHA256": hashlib.sha256(config.read_bytes()).hexdigest()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--binary", type=Path, required=True)
    args = parser.parse_args()
    if not args.live or not args.output.is_absolute() or not args.binary.is_absolute():
        parser.error("explicit --live and absolute private output/binary paths required")
    os.umask(0o077)
    args.output.mkdir(mode=0o700)
    credential = None
    for line in Path("/home/kirill/p/CLIProxyAPI-setup/.env").read_text().splitlines():
        if line.startswith("CPA_API_KEY="):
            parts = shlex.split(line.split("=", 1)[1], comments=True)
            credential = parts[0] if len(parts) == 1 else None
            break
    if not credential:
        raise RuntimeError("canonical CPA credential missing; values suppressed")
    env = os.environ.copy()
    env.pop("OPENAI_API_KEY", None)
    env["CPA_API_KEY"] = credential
    initial = runtime()
    atomic(args.output / "environment-before.json", initial)
    ledger = {"allowance": 10000000, "reportedTokens": 0, "unknownReserve": 0,
              "status": "running", "runs": [], "nativeInternalUsage": None,
              "priorCampaignReportedTokens": 3235681, "priorCampaignUnknownReserve": 163840}
    atomic(args.output / "campaign-budget.json", ledger)
    # Every main sample crosses the 200-start estimate. ABBA mitigates order drift.
    cases = [("pilot-short", 2, 0, 0), ("pilot-input", 2, 1024, 0),
             ("pilot-output", 2, 0, 1), ("pilot-combined", 2, 1024, 1),
             ("a-short-1", 256, 0, 0), ("b-input-1", 256, 1024, 0),
             ("b-input-2", 256, 1024, 0), ("a-short-2", 256, 0, 0),
             ("c-output", 256, 0, 1), ("d-combined", 256, 1024, 1)]
    for name, calls, words, long_every in cases:
        if runtime() != initial:
            ledger.update(status="stopped", stopReason="runtime_identity_changed")
            break
        remaining = ledger["allowance"] - ledger["reportedTokens"] - ledger["unknownReserve"]
        if remaining < 32768 * min(calls, 128):
            ledger.update(status="stopped", stopReason="insufficient_reserved_budget")
            break
        quiet = "0s" if name.startswith("pilot-") else "90s"
        command = [str(args.binary), "--live", "--output", str(args.output / name),
                   "--calls", str(calls), "--competing=false", "--policy", "windowed",
                   "--context-words", str(words), "--long-every", str(long_every),
                   "--token-budget", str(min(8000000, remaining)), "--quiet", quiet,
                   "--deadline", "15m", "--endpoint", "public"]
        row = {"case": name, "calls": calls, "contextWords": words,
               "longEvery": long_every, "command": command, "status": "running"}
        ledger["runs"].append(row)
        atomic(args.output / "campaign-budget.json", ledger)
        print(json.dumps({"phase": "case-start", "case": name}), flush=True)
        with (args.output / (name + ".log")).open("wb") as log:
            process = subprocess.Popen(command, env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                while process.poll() is None:
                    time.sleep(5)
                    snapshot = args.output / name / "status.json"
                    progress = json.loads(snapshot.read_text()) if snapshot.exists() else {}
                    row["latestProgress"] = progress
                    atomic(args.output / "campaign-budget.json", ledger)
                    print(json.dumps({"phase": "case-progress", "case": name,
                                      "elapsedMs": progress.get("elapsedMs"),
                                      "actors": progress.get("Actors"),
                                      "reportedTokens": progress.get("ReportedTokens"),
                                      "stopReason": progress.get("stopReason")}), flush=True)
            except BaseException:
                process.terminate()
                process.wait(timeout=190)
                raise
        summary_path = args.output / name / "summary.json"
        summary = json.loads(summary_path.read_text()) if summary_path.exists() else {}
        measurement = summary.get("measurement", row.get("latestProgress", {}))
        reported = measurement.get("ReportedTokens", 0)
        reserve = measurement.get("UnknownTokenReserve", 0) + measurement.get("ActiveTokenReserve", 0)
        row.update(status=summary.get("status", "failed"), exitCode=process.returncode,
                   reportedTokens=reported, unknownReserve=reserve)
        row.pop("latestProgress", None)
        ledger["reportedTokens"] += reported
        ledger["unknownReserve"] += reserve
        actors = measurement.get("Actors", [])
        reason = measurement.get("stopReason", "")
        if any(a.get("Policy", 0) for a in actors) or reason or not summary or (name.startswith("pilot-") and row["status"] != "passed"):
            ledger.update(status="stopped", stopReason=reason or "qualification_or_artifact_failure")
            break
        atomic(args.output / "campaign-budget.json", ledger)
    else:
        ledger["status"] = "finished"
    after = runtime()
    atomic(args.output / "environment-after.json", after)
    ledger["runtimeUnchanged"] = after == initial
    if not ledger["runtimeUnchanged"]:
        ledger.update(status="stopped", stopReason="runtime_identity_changed")
    atomic(args.output / "campaign-budget.json", ledger)
    print(json.dumps({"phase": "campaign-finished", "status": ledger["status"],
                      "reportedTokens": ledger["reportedTokens"],
                      "unknownReserve": ledger["unknownReserve"]}), flush=True)
    return 0 if ledger["status"] == "finished" else 1


if __name__ == "__main__":
    raise SystemExit(main())

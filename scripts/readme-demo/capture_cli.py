#!/usr/bin/env python3
"""Check real shell capture output and redact session credentials."""
import argparse
import json
import os
from pathlib import Path
import socket
import time
import urllib.error
import urllib.request

MODEL = "openai/gpt-6.1-sol"
PROVIDER = "openai"
PROVIDER_MODEL = "gpt-6.1-sol"


def safe_banner(text):
    """Keep the CLI banner and remove both session credentials."""
    result = []
    for line in text.splitlines():
        if line.startswith("Gateway API key (shown once): "):
            line = "Gateway API key (shown once): [value hidden]"
        if line.startswith("Console (one-time launch link): "):
            line = "Console (one-time launch link): [value hidden]"
        if line.startswith(("Starport development gateway", "URL: ", "Authentication: ",
                            "Gateway API key (shown once): ", "Console (one-time launch link): ")):
            result.append(line)
    return "\n".join(result)


def check_isolation():
    with socket.socket() as probe:
        probe.settimeout(1)
        try:
            probe.connect(("1.1.1.1", 443))
        except PermissionError:
            Path("isolation.txt").write_text("PASS\n")
            return
        raise RuntimeError("the capture requires the loopback-only sandbox")


def ready():
    deadline = time.monotonic() + 60
    pid = int(Path("gateway.pid").read_text())
    url = "http://127.0.0.1:19335"
    while time.monotonic() < deadline:
        os.kill(pid, 0)
        text = Path("gateway.log").read_text()
        key = next((line.split(": ", 1)[1] for line in text.splitlines()
                    if line.startswith("Gateway API key (shown once): ")), None)
        try:
            with urllib.request.urlopen(url + "/health/ready", timeout=1) as response:
                if key and response.status == 200:
                    Path("gateway.key").write_text(key)
                    Path("gateway.key").chmod(0o600)
                    Path("gateway-banner.txt").write_text(safe_banner(text) + "\n")
                    return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.1)
    raise RuntimeError("the gateway did not become ready")


def selected_model(discovery):
    """Require the canonical model and its exact OpenAI chat offering."""
    model = next((value for value in discovery["models"] if value["id"] == MODEL), None)
    if model is None:
        raise RuntimeError("the selected canonical model is absent")
    offering = next((value for value in model["offerings"]
                     if value["provider"] == PROVIDER and value["provider_model_id"] == PROVIDER_MODEL), None)
    if offering is None:
        raise RuntimeError("the exact OpenAI provider model is absent")
    if "chat-completions" not in offering["operations"]:
        raise RuntimeError("the selected offering does not declare chat completions")
    return model, offering


def closing():
    """Prove that the gateway remains live before the visible result."""
    pid = int(Path("gateway.pid").read_text())
    os.kill(pid, 0)
    url = "http://127.0.0.1:19335"
    key = Path("gateway.key").read_text()
    with urllib.request.urlopen(url + "/health/ready", timeout=5) as response:
        health = json.loads(response.read())
        if response.status != 200 or health["status"] != "ok":
            raise RuntimeError("the gateway is not ready at the ending")
    request = urllib.request.Request(url + "/api/v1/catalog/discovery",
                                     headers={"Authorization": "Bearer " + key})
    with urllib.request.urlopen(request, timeout=5) as response:
        discovery = json.loads(response.read())
        if response.status != 200:
            raise RuntimeError("the authenticated catalog did not return 200")
        _, offering = selected_model(discovery)
    try:
        urllib.request.urlopen(url + "/api/v1/catalog/discovery", timeout=5)
    except urllib.error.HTTPError as error:
        unauthorized_status = error.code
    else:
        raise RuntimeError("the catalog accepted an anonymous request")
    if unauthorized_status != 401:
        raise RuntimeError("the anonymous catalog request did not return 401")
    Path("closing.json").write_text(json.dumps({"gateway_live": True, "health_status": 200,
                                               "authenticated_catalog_status": 200,
                                               "anonymous_catalog_status": unauthorized_status,
                                               "selected_model": MODEL, "provider": offering["provider"],
                                               "provider_model_id": offering["provider_model_id"]}, indent=2) + "\n")


def verify():
    if Path("isolation.txt").read_text().strip() != "PASS":
        raise RuntimeError("the capture did not prove network isolation")
    health = json.loads(Path("health.json").read_text())
    discovery = json.loads(Path("discovery.json").read_text())
    selected, offering = selected_model(discovery)
    if health["status"] != "ok" or not discovery["generation_id"] or not selected["offerings"]:
        raise RuntimeError("the gateway API output did not pass")
    if Path("shutdown.txt").read_text().strip() != "0":
        raise RuntimeError("the gateway did not stop cleanly")
    pid = int(Path("gateway.pid").read_text())
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        pass
    else:
        raise RuntimeError("the gateway process remains")
    home = Path(os.environ["HOME"])
    files = [str(path.relative_to(home)) for path in home.rglob("*") if path.is_file()]
    if files:
        raise RuntimeError("the development gateway kept home files")
    report = {"kind": "current-source-cli", "real_provider": False, "verdict": "PASS",
              "network_egress_denied": True, "provider_credentials_present": False,
              "shutdown_exit_code": 0, "leftover_home_files": files,
              "health": health, "discovery": {"generation_id": discovery["generation_id"], "model": selected, "selected_offering": offering},
              "visible_ending": json.loads(Path("closing.json").read_text())}
    Path("capture.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("ready", "verify", "isolation", "closing"))
    args = parser.parse_args()
    {"ready": ready, "verify": verify, "isolation": check_isolation, "closing": closing}[args.action]()

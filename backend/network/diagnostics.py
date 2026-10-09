from __future__ import annotations

import platform
import re
import shutil
import socket
import subprocess
import time


def _run(cmd: list[str], timeout: int = 12) -> str:
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, check=False)
        return (result.stdout + "\n" + result.stderr).strip()
    except FileNotFoundError:
        return "Не установлена системная утилита"
    except subprocess.TimeoutExpired:
        return "Истекло время ожидания"
    except OSError as exc:
        return str(exc)


def _gateway() -> str | None:
    system = platform.system().lower()
    commands = [["powershell", "-NoProfile", "-Command", "(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1 -ExpandProperty NextHop)"]]
    if system != "windows":
        commands = [["ip", "route", "show", "default"], ["route", "-n"]]
    for cmd in commands:
        output = _run(cmd, 5)
        if "Не установлена" in output:
            continue
        if system == "windows":
            match = re.search(r"\b(?:\d{1,3}\.){3}\d{1,3}\b", output)
        else:
            match = re.search(r"default via ((?:\d{1,3}\.){3}\d{1,3})", output) or re.search(r"^0\.0\.0\.0\s+((?:\d{1,3}\.){3}\d{1,3})", output, re.M)
        if match:
            return match.group(1) if match.lastindex else match.group(0)
    return None


def _ping(host: str) -> dict:
    system = platform.system().lower()
    cmd = ["ping", "-n", "2", "-w", "1400", host] if system == "windows" else ["ping", "-c", "2", "-W", "2", host]
    text = _run(cmd, 7)
    if "Не установлена" in text:
        return {"ok": False, "result": "Команда ping не найдена"}
    lower = text.lower()
    loss = re.search(r"(\d+)%\s*(?:loss|потер)|(?:loss|потер)[^\d]*(\d+)%", lower)
    times = [float(x.replace(",", ".")) for x in re.findall(r"(?:time[=<]|время[=<])\s*([\d,.]+)\s*ms", lower)]
    result = f"Потери {loss.group(1) or loss.group(2)}%" if loss else ("Ответ получен" if "ttl=" in lower or "ttl=" in text.lower() else "Нет ответа")
    if times:
        result += f" · {sum(times)/len(times):.1f} мс"
    return {"ok": "ttl=" in lower, "result": result}


def run_diagnostics() -> dict:
    results = []
    gateway = _gateway()
    if gateway:
        result = _ping(gateway)
        results.append({"name": f"Шлюз {gateway}", **result})
    else:
        results.append({"name": "Шлюз", "ok": False, "result": "Не удалось определить маршрут по умолчанию"})
    for address in ("1.1.1.1", "8.8.8.8"):
        results.append({"name": f"Ping {address}", **_ping(address)})
    try:
        started = time.monotonic()
        answers = socket.getaddrinfo("example.com", 443, type=socket.SOCK_STREAM)
        dns_time = (time.monotonic() - started) * 1000
        address = answers[0][4][0] if answers else ""
        results.append({"name": "DNS example.com", "ok": bool(answers), "result": f"{address} · {dns_time:.0f} мс"})
    except OSError as exc:
        results.append({"name": "DNS example.com", "ok": False, "result": str(exc)[:110]})
    system = platform.system().lower()
    if system == "windows":
        trace = _run(["tracert", "-d", "-h", "5", "-w", "700", "1.1.1.1"], 9)
    else:
        tool = "tracepath" if shutil.which("tracepath") else "traceroute"
        trace = _run([tool, "-n", "-m", "5", "-w", "1", "1.1.1.1"], 9)
    trace_result = next((line.strip() for line in trace.splitlines() if re.search(r"\d+\s+[*\d]", line)), "Маршрут скрыт или утилита недоступна")
    results.append({"name": "Traceroute 1.1.1.1", "ok": "не установлена" not in trace_result.lower(), "result": trace_result[:150]})
    return {"results": results, "checked_at": time.time()}

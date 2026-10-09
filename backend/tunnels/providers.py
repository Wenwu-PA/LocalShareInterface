from __future__ import annotations

import ipaddress
import os
import re
import shutil
import socket
import subprocess
import threading
import time
from pathlib import Path
from typing import Protocol
from urllib.parse import urlparse

from ..config import CONFIG


class TunnelProvider(Protocol):
    name: str
    def status(self) -> dict: ...
    def start(self) -> dict: ...
    def stop(self) -> dict: ...


class CloudflareQuickTunnel:
    name = "cloudflare"
    def __init__(self):
        self.process: subprocess.Popen | None = None
        self.url: str | None = None
        self.error: str | None = None
        self.started_at: float | None = None
        self._ready = threading.Event()
        self._lock = threading.RLock()
        self.requests = 0
        self.bytes_in = 0
        self.bytes_out = 0

    def _read_output(self, process: subprocess.Popen) -> None:
        try:
            for line in process.stdout:
                match = re.search(r"https://[a-zA-Z0-9-]+\.trycloudflare\.com", line)
                if match:
                    with self._lock:
                        self.url = match.group(0)
                        self._ready.set()
        except (OSError, ValueError):
            pass
        finally:
            if process.poll() is not None:
                with self._lock:
                    if not self.url:
                        self.error = f"cloudflared завершился с кодом {process.returncode}"
                    self._ready.set()

    def status(self) -> dict:
        with self._lock:
            if self.process and self.process.poll() is not None:
                if not self.url:
                    self.error = self.error or f"cloudflared остановился (код {self.process.returncode})"
            alive = bool(self.process and self.process.poll() is None)
            label = "Включён" if alive and self.url else ("Подключается" if alive else ("Ошибка" if self.error else "Выключен"))
            return {"provider": self.name, "enabled": alive, "status": label,
                    "url": self.url if alive else None, "started_at": self.started_at if alive else None,
                    "uptime_seconds": max(0, int(time.time() - self.started_at)) if alive and self.started_at else 0,
                    "requests": self.requests, "bytes_in": self.bytes_in, "bytes_out": self.bytes_out, "error": self.error}

    def available(self) -> bool:
        binary_config = str(CONFIG["tunnel"].get("cloudflared_binary", "cloudflared"))
        return bool(shutil.which(binary_config) or Path(binary_config).is_file())

    def start(self) -> dict:
        with self._lock:
            if self.process and self.process.poll() is None:
                return self.status()
            config = CONFIG["tunnel"]
            scheme = "https" if CONFIG["server"].get("https", True) else "http"
            origin = str(config.get("origin_url") or f"{scheme}://127.0.0.1:{CONFIG['server'].get('port', 8765)}")
            parsed = urlparse(origin)
            if parsed.scheme != "https" or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
                raise RuntimeError("Туннель разрешено направлять только на локальный HTTPS-origin")
            binary_config = str(config.get("cloudflared_binary", "cloudflared"))
            binary = shutil.which(binary_config)
            if not binary and Path(binary_config).is_file():
                binary = str(Path(binary_config).resolve())
            if not binary:
                self.error = "cloudflared не найден. Установите Cloudflare Tunnel и задайте cloudflared_binary в config.yaml"
                raise RuntimeError("cloudflared не найден. Установите Cloudflare Tunnel и задайте cloudflared_binary в config.yaml")
            self.url = None
            self.error = None
            self._ready.clear()
            command = [binary, "tunnel", "--no-autoupdate", "--url", origin, "--no-tls-verify"]
            flags = getattr(subprocess, "CREATE_NO_WINDOW", 0) if os.name == "nt" else 0
            self.process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                            stdin=subprocess.DEVNULL, text=True, encoding="utf-8", errors="replace",
                                            bufsize=1, creationflags=flags)
            self.started_at = time.time()
            reader = threading.Thread(target=self._read_output, args=(self.process,), daemon=True, name="lanbridge-cloudflared")
            reader.start()
        self._ready.wait(22)
        status = self.status()
        if not status["enabled"]:
            error = status["error"] or "Cloudflare Quick Tunnel не выдал адрес за 22 секунды"
            self.stop()
            raise RuntimeError(error)
        return status

    def stop(self) -> dict:
        with self._lock:
            process = self.process
            self.url = None
            self.started_at = None
            self.error = None
            self.process = None
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=4)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=4)
        return self.status()

    def record_request(self, bytes_in: int = 0, bytes_out: int = 0) -> None:
        with self._lock:
            self.requests += 1
            self.bytes_in += max(0, bytes_in)
            self.bytes_out += max(0, bytes_out)

    def record_response_bytes(self, size: int) -> None:
        with self._lock:
            self.bytes_out += max(0, size)


class UnavailableProvider:
    def __init__(self, name: str, description: str):
        self.name = name
        self.description = description

    def status(self) -> dict:
        return {"provider": self.name, "enabled": False, "status": "Требуется настройка", "url": None,
                "uptime_seconds": 0, "requests": 0, "bytes_in": 0, "bytes_out": 0,
                "error": self.description}

    def start(self) -> dict:
        raise RuntimeError(self.description)

    def stop(self) -> dict:
        return self.status()


class TunnelManager:
    def __init__(self):
        self.providers: dict[str, TunnelProvider] = {
            "cloudflare": CloudflareQuickTunnel(),
            "netbird": UnavailableProvider("netbird", "Подключение к существующей сети NetBird пока не настроено"),
            "reverse_proxy": UnavailableProvider("reverse_proxy", "Нужен внешний HTTPS reverse proxy и отдельная политика firewall"),
        }
        configured = str(CONFIG["tunnel"].get("provider", "cloudflare"))
        self.active_name = configured if configured in self.providers else "cloudflare"

    def status(self) -> dict:
        return self.providers[self.active_name].status()

    def start(self) -> dict:
        return self.providers[self.active_name].start()

    def stop(self) -> dict:
        return self.providers[self.active_name].stop()

    def is_trusted_proxy(self, peer_ip: str | None, headers) -> bool:
        status = self.status()
        if not status["enabled"] or not headers.get("cf-ray") or not peer_ip:
            return False
        try:
            return ipaddress.ip_address(peer_ip).is_loopback
        except ValueError:
            return False

    def record_request(self, bytes_in: int, bytes_out: int) -> None:
        provider = self.providers.get(self.active_name)
        if isinstance(provider, CloudflareQuickTunnel):
            provider.record_request(bytes_in, bytes_out)


manager = TunnelManager()


def external_network_check(port: int) -> dict:
    local_ip = None
    probe = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        probe.connect(("1.1.1.1", 53))
        local_ip = probe.getsockname()[0]
    except OSError:
        local_ip = "127.0.0.1"
    finally:
        probe.close()
    try:
        import urllib.request
        with urllib.request.urlopen("https://www.cloudflare.com/cdn-cgi/trace", timeout=5) as response:
            body = response.read(4096).decode("utf-8", "replace")
        public_ip = next((line[3:] for line in body.splitlines() if line.startswith("ip=")), None)
    except Exception:
        public_ip = None
    local = ipaddress.ip_address(local_ip)
    cgnat_range = ipaddress.ip_network("100.64.0.0/10")
    if local in cgnat_range:
        nat = "Обнаружен адрес CGNAT (100.64.0.0/10) на сервере"
    elif local.is_private and public_ip:
        nat = "Сервер за частным адресом/NAT. По одному адресу сервера отличить обычный NAT от CGNAT роутера нельзя; сравните WAN IP роутера с внешним адресом."
    elif public_ip and not local.is_private:
        nat = "На интерфейсе сервера виден публичный адрес; входящий порт всё равно может блокировать firewall."
    else:
        nat = "Внешний IP не получен. Проверьте интернет и вручную сравните WAN IP роутера."
    try:
        public_is_private = not public_ip or ipaddress.ip_address(public_ip).is_private
    except ValueError:
        public_is_private = True
    port_status = "TCP-порт LANBridge слушает на сервере; доступность из интернета этим тестом не подтверждается"
    return {"local_ip": local_ip, "public_ip": public_ip, "port": int(port), "port_status": port_status,
            "nat": nat, "gray_ip_warning": "" if not public_is_private else "Адрес выглядит частным или не удалось определить внешний IP; прямой входящий доступ может быть недоступен."}

from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parent.parent


def load_dotenv() -> None:
    env_file = Path(os.environ.get("LANBRIDGE_ENV_FILE", ROOT / ".env"))
    if not env_file.is_absolute():
        env_file = ROOT / env_file
    if not env_file.is_file():
        return
    for raw in env_file.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip().strip('"').strip("'")
        if key.strip():
            os.environ.setdefault(key.strip(), value)


def load_config() -> dict[str, Any]:
    load_dotenv()
    path = Path(os.environ.get("LANBRIDGE_CONFIG", ROOT / "config.yaml"))
    if not path.is_absolute():
        path = ROOT / path
    if not path.exists() and "LANBRIDGE_CONFIG" not in os.environ:
        shutil.copyfile(ROOT / "config.example.yaml", path)
    config = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    for section in ("server", "storage", "security", "tunnel", "monitoring"):
        config.setdefault(section, {})
    env = {"LANBRIDGE_HOST": ("server", "host", str),
           "LANBRIDGE_PORT": ("server", "port", int),
           "LANBRIDGE_CLOUDFLARED": ("tunnel", "cloudflared_binary", str),
           "LANBRIDGE_TLS_CERTFILE": ("server", "tls_certfile", str),
           "LANBRIDGE_TLS_KEYFILE": ("server", "tls_keyfile", str)}
    for name, (section, key, cast) in env.items():
        if os.environ.get(name):
            config[section][key] = cast(os.environ[name])
    for section, key in (("storage", "root"), ("storage", "database"), ("storage", "tls_dir"), ("storage", "logs")):
        value = Path(config[section][key])
        config[section][key] = value if value.is_absolute() else ROOT / value
    for key in ("tls_certfile", "tls_keyfile"):
        value = config["server"].get(key, "")
        if value:
            cert_path = Path(value)
            config["server"][key] = cert_path if cert_path.is_absolute() else ROOT / cert_path
    return config


CONFIG = load_config()

"""Capture the real UI using temporary demo files and a disposable account."""
from __future__ import annotations

from pathlib import Path
import secrets
import sys
import zipfile

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from scripts.smoke import test_server  # noqa: E402
from scripts.api_example import run_example  # noqa: E402


def main() -> None:
    output = ROOT / "docs" / "screenshots"
    output.mkdir(parents=True, exist_ok=True)
    with test_server() as (base, temporary):
        share = temporary / "files"
        (share / "Документы").mkdir()
        (share / "Фото с телефона").mkdir()
        (share / "Заметки.md").write_text("# Обмен в локальной сети\n\nФайлы доступны с компьютера и телефона.\n", encoding="utf-8")
        (share / "Показатели.csv").write_text("Дата;Файлы;Объём\n2026-10-08;12;256\n", encoding="utf-8")
        (share / "Инструкция.txt").write_text("Выберите файл для скачивания или создайте временную ссылку.\n", encoding="utf-8")
        with zipfile.ZipFile(share / "Материалы.zip", "w") as archive:
            archive.writestr("readme.txt", "LANBridge demo archive")
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1000}, locale="ru-RU")
            page = context.new_page()
            errors: list[str] = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.on("response", lambda response: errors.append(f"{response.status} {response.url}")
                    if response.url.startswith(base + "/api/") and response.status >= 400 else None)
            page.goto(base)
            page.get_by_role("button", name="Создать администратора").wait_for()
            page.locator('[name="username"]').fill("demo")
            password = secrets.token_urlsafe(24)
            page.locator('[name="password"]').fill(password)
            page.locator("#auth-submit").click()
            page.wait_for_url("**/app")
            page.locator("#file-rows tr").first.wait_for()
            assert run_example(base, "demo", password, self_signed=True)["files"] == 6
            for section, selector in (("devices", "#page-root h1"), ("monitoring", '[data-metric="cpu"]'),
                                      ("external", '[data-action="toggle-tunnel"]'), ("history", "#history-q"),
                                      ("settings", "#users-list")):
                page.locator(f'.nav-item[data-page="{section}"]').click()
                page.locator(selector).wait_for()
            page.locator('.nav-item[data-page="files"]').click()
            page.locator("#file-rows tr").first.wait_for()
            for width, height, device in ((1440, 1000, "desktop"), (390, 844, "mobile")):
                page.set_viewport_size({"width": width, "height": height})
                for theme in ("dark", "light"):
                    is_light = page.locator("body").evaluate("element => element.classList.contains('theme-light')")
                    if is_light != (theme == "light"):
                        page.locator("#theme-toggle").click()
                    page.screenshot(path=str(output / f"{device}-{theme}.png"), full_page=True, animations="disabled")
                    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), device
            browser.close()
            assert not errors, errors
    print("PASS: admin navigation; four desktop/mobile theme screenshots saved")


if __name__ == "__main__":
    main()

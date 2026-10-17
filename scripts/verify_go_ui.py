"""Real-browser checks and review images for the missing design screens."""
from pathlib import Path
import gzip
import re
import json
import os
import secrets
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
from PIL import Image, ImageDraw, ImageFont

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]


def main():
    output = ROOT / "docs" / "previews"
    output.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="lanbridge-go-ui-") as folder:
        temporary = Path(folder)
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        config = temporary / "config.yaml"
        config.write_text(f"server:\n  port: {port}\n", encoding="utf-8")
        env = {key: value for key, value in os.environ.items() if not key.startswith("LANBRIDGE_GO_")}
        base = f"https://127.0.0.1:{port}"
        with (temporary / "server.log").open("w", encoding="utf-8") as log:
            process = subprocess.Popen([str(ROOT / "dist" / "lanbridge.exe"), "-config", str(config)], env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        with urllib.request.urlopen(base + "/healthz", context=ssl._create_unverified_context(), timeout=1):
                            break
                    except OSError:
                        time.sleep(.1)
                with sync_playwright() as playwright:
                    browser = playwright.chromium.launch()
                    context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1000}, color_scheme="dark")
                    page = context.new_page()
                    errors, external = [], []
                    page.on("pageerror", lambda error: errors.append(str(error)))
                    page.on("request", lambda request: external.append(request.url) if not request.url.startswith(base) else None)
                    started = time.monotonic()
                    page.goto(base)
                    page.get_by_role("button", name="Создать администратора", exact=True).wait_for()
                    ready_ms = round((time.monotonic() - started) * 1000)

                    def capture(screen):
                        if page.locator(".workspace").count():
                            page.locator(".live").wait_for(state="attached")
                        page.wait_for_function("() => document.querySelectorAll('.toast').length === 0")
                        for width, device in ((1440, "desktop"), (390, "mobile")):
                            page.set_viewport_size({"width": width, "height": 1000})
                            for theme in ("dark", "light"):
                                if page.locator("html").get_attribute("data-theme") != theme:
                                    page.get_by_role("button", name="Переключить тему", exact=True).filter(visible=True).click()
                                page.wait_for_function("theme => document.documentElement.dataset.theme === theme", arg=theme)
                                page.evaluate("document.fonts.ready")
                                page.evaluate("() => { document.activeElement?.blur(); window.scrollTo(0, 0); }")
                                assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), screen
                                page.screenshot(path=str(output / f"{screen}-{device}-{theme}.png"), full_page=True, animations="disabled")
                        page.set_viewport_size({"width": 1440, "height": 1000})

                    capture("setup")
                    password = secrets.token_urlsafe(24)
                    page.locator('[name="username"]').fill("reviewadmin")
                    page.locator('[name="password"]').fill(password)
                    page.locator('[name="confirm"]').fill(password)
                    page.get_by_role("button", name="Создать администратора", exact=True).click()
                    page.locator(".workspace").wait_for()
                    page.locator(".live").wait_for()
                    page.get_by_role("button", name="Добавить проверку", exact=True).click()
                    page.locator("dialog").get_by_label("Название", exact=True).fill("UI TCP check")
                    page.locator("dialog").get_by_label("Адрес host:port", exact=True).fill(f"127.0.0.1:{port}")
                    page.get_by_role("button", name="Сохранить проверку", exact=True).click()
                    page.locator(".monitor-row").filter(has_text="UI TCP check").get_by_text("Работает", exact=True).wait_for()
                    with page.expect_response(lambda response: '/api/monitors/history?' in response.url and 'group=LAN' in response.url) as chart_response:
                        page.get_by_label('Группа графика', exact=True).select_option('LAN')
                    assert chart_response.value.status == 200
                    with page.expect_response(lambda response: '/api/monitors/history?' in response.url and 'window=7d' in response.url and 'group=LAN' in response.url):
                        page.get_by_label('Период графика', exact=True).select_option('7d')
                    assert page.get_by_label('Группа графика', exact=True).input_value() == 'LAN'
                    delayed = []
                    def history_fixture(route):
                        url = route.request.url
                        if 'group=LAN' not in url:
                            return route.continue_()
                        if 'window=24h' in url:
                            delayed.append(route)
                            return
                        route.fulfill(json={'points':[{'at':100,'latency':70,'total':1,'up':1}], 'samples':1,'availability':100})
                    page.route('**/api/monitors/history?*', history_fixture)
                    page.get_by_label('Период графика',exact=True).select_option('24h')
                    deadline = time.monotonic()+3
                    while not delayed and time.monotonic()<deadline:
                        page.wait_for_timeout(10)
                    assert delayed, 'old history request was not intercepted'
                    page.get_by_label('Период графика',exact=True).select_option('7d')
                    page.locator('.latency-chart small').filter(has_text='70 мс').wait_for()
                    with page.expect_response(lambda response:'window=24h' in response.url and 'group=LAN' in response.url):
                        delayed.pop().fulfill(json={'points':[{'at':100,'latency':900,'total':1,'up':1}], 'samples':1,'availability':100})
                    page.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
                    assert page.locator('.latency-chart small').inner_text()=='70 мс', 'stale history replaced current chart'
                    page.unroute('**/api/monitors/history?*',history_fixture)
                    page.set_viewport_size({'width':390,'height':1000})
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'chart controls overflow mobile'
                    page.set_viewport_size({'width':1440,'height':1000})
                    page.get_by_label('Период графика', exact=True).select_option('1h')
                    page.get_by_label('Группа графика', exact=True).select_option('')
                    page.get_by_label('Группа проверок',exact=True).select_option('wan')
                    page.wait_for_function("()=>document.querySelectorAll('.monitor-row').length===0")
                    page.get_by_label('Группа проверок',exact=True).select_option('lan')
                    page.locator('.monitor-row').filter(has_text='UI TCP check').wait_for()
                    page.get_by_label('Группа проверок',exact=True).select_option('')
                    page.get_by_role("checkbox", name="Выбрать UI TCP check", exact=True).check()
                    page.get_by_role("button", name="Пауза", exact=True).click()
                    page.locator(".monitor-row").filter(has_text="UI TCP check").get_by_text("Пауза", exact=True).wait_for()
                    page.get_by_role("checkbox", name="Выбрать UI TCP check", exact=True).check()
                    page.get_by_role("button", name="Продолжить", exact=True).click()
                    page.locator(".monitor-row").filter(has_text="UI TCP check").get_by_text("Работает", exact=True).wait_for()
                    for screen in ("devices", "settings", "missing"):
                        page.goto(base + "/#" + screen)
                        page.locator(".workspace").wait_for()
                        page.locator(".live").wait_for()
                        if screen == "devices":
                            page.get_by_role("heading", name="Обнаружение в локальной сети", exact=True).wait_for()
                            interfaces = page.request.get(base + "/api/devices").json()["interfaces"]
                            if interfaces:
                                own_ip = interfaces[0]["ip"]
                                page.get_by_label("Подсеть IPv4", exact=True).fill(own_ip + "/32")
                                page.get_by_role("button", name="Сканировать сеть", exact=True).click()
                                page.locator(".page-content table tbody tr").filter(has_text=own_ip).locator(".monitor-name").wait_for(timeout=20000)
                                page.locator(".page-content table tbody tr").filter(has_text=own_ip).locator(".monitor-name").click()
                                page.get_by_label("Имя устройства", exact=True).fill("Тестовый сервер")
                                page.get_by_label("Известное устройство", exact=True).check()
                                page.get_by_role("button", name="Сохранить устройство", exact=True).click()
                                page.locator(".page-content table .monitor-name").filter(has_text="Тестовый сервер").wait_for()
                            page.get_by_label("Проверка", exact=True).select_option("port")
                            page.get_by_label("Цель диагностики", exact=True).fill(f"127.0.0.1:{port}")
                            page.get_by_role("button", name="Запустить диагностику", exact=True).click()
                            page.locator(".diagnostic-output").filter(has_text="OK").wait_for()
                            page.get_by_label("Проверка", exact=True).select_option("speed")
                            page.get_by_role("button", name="Запустить диагностику", exact=True).click()
                            page.locator(".diagnostic-output").filter(has_text="16.0 МиБ").wait_for()
                            page.get_by_label("Проверка", exact=True).select_option("ping")
                            page.get_by_label("Цель диагностики", exact=True).fill("127.0.0.1")
                            page.get_by_role("button", name="Запустить диагностику", exact=True).click()
                            page.get_by_role("button", name="Отменить диагностику", exact=True).click()
                            page.locator(".diagnostic-output").filter(has_text="Отменено").wait_for()
                        if screen == "settings":
                            page.get_by_role('button', name='Интернет — выше', exact=True).click()
                            page.get_by_label("Название сети", exact=True).fill("Моя локальная сеть")
                            page.get_by_role("button", name="Сохранить настройки", exact=True).click()
                            page.get_by_text("Настройки сохранены", exact=True).wait_for()
                            page.reload()
                            page.get_by_label("Название сети", exact=True).wait_for()
                            assert page.get_by_label("Название сети", exact=True).input_value() == "Моя локальная сеть"
                            other = context.new_page()
                            other.goto(base + "/#overview")
                            other.locator('.page-content .stats.four > .stat').first.get_by_role('heading', name='ИНТЕРНЕТ', exact=True).wait_for()
                            page.get_by_role('button', name='Интернет — ниже', exact=True).click()
                            other.locator(".live").wait_for()
                            page.get_by_label("Название сети", exact=True).fill("Домашняя сеть")
                            page.get_by_role("button", name="Сохранить настройки", exact=True).click()
                            other.locator(".network-label strong").filter(has_text="Домашняя сеть").wait_for()
                            other.locator('.page-content .stats.four > .stat').first.get_by_role('heading', name='ДОСТУПНОСТЬ / 30 ДНЕЙ', exact=True).wait_for()
                            logo_path = temporary / 'logo.png'
                            Image.new('RGB', (32,32), '#568fff').save(logo_path)
                            page.get_by_label('Загрузить логотип',exact=True).set_input_files(str(logo_path))
                            other.locator('.sidebar .brand-logo').wait_for()
                            page.reload()
                            page.locator('.sidebar .brand-logo').wait_for()
                            assert page.locator('.sidebar .brand-logo').evaluate('(img)=>img.complete && img.naturalWidth===32')
                            page.get_by_role('button',name='Удалить логотип',exact=True).click()
                            other.locator('.sidebar .brand-logo').wait_for(state='detached')
                            page.locator('.sidebar .brand-logo').wait_for(state='detached')
                            other.close()
                            page.get_by_role("button", name="Добавить пользователя", exact=True).click()
                            page.locator('dialog [name="username"]').fill("reviewuser")
                            page.locator('dialog [name="password"]').fill(secrets.token_urlsafe(24))
                            page.locator('dialog button[type="submit"]').click()
                            page.get_by_text("Пользователь создан", exact=True).wait_for()
                        capture("error" if screen == "missing" else screen)
                    page.get_by_role("button", name="Выйти", exact=True).click()
                    page.get_by_role("button", name="Войти", exact=True).wait_for()
                    capture("login")
                    # A real login from the rendered form, then mobile navigation.
                    page.locator('[name="username"]').fill("reviewadmin")
                    page.locator('[name="password"]').fill(password)
                    page.get_by_role("button", name="Войти", exact=True).click()
                    page.locator(".workspace").wait_for()
                    page.set_viewport_size({"width": 390, "height": 1000})
                    page.get_by_role("button", name="Ещё", exact=True).click()
                    page.locator('.mobile-more a[href="#settings"]').click()
                    page.get_by_role("heading", name="Настройки", exact=True).wait_for()
                    context.set_offline(True)
                    page.locator(".connection-note").wait_for()
                    assert page.get_by_role("heading", name="Настройки", exact=True).is_visible()
                    context.set_offline(False)
                    page.locator(".live").wait_for(state="attached")
                    page.locator(".connection-note").wait_for(state="detached")
                    assert not external, external
                    assert not errors, errors
                    browser.close()
                log.flush()
                assert '"level":"ERROR"' not in (temporary / "server.log").read_text(), "Server error"
            finally:
                process.terminate()
                process.wait(timeout=10)
    initial_paths={"index.html"}|set(re.findall(r'(?:src|href)="/([^"<>]+)"',(ROOT/"web/dist/index.html").read_text()))
    assets = [p for p in (ROOT / "web" / "dist").rglob("*") if p.suffix in {".html", ".css", ".js", ".mjs"}]
    names = [("devices", "Устройства"), ("settings", "Настройки"), ("login", "Вход"),
             ("setup", "Первый запуск"), ("error", "Страница ошибки")]
    if (output / "files-desktop-dark.png").exists():
        names.append(("files", "Файлы"))
    cards = []
    for key, title in names:
        images = "".join(f'<a href="{key}-{device}-{theme}.png"><img loading="lazy" src="{key}-{device}-{theme}.png" alt="{title}: {device}, {theme}"><span>{device} · {theme}</span></a>'
                         for device in ("desktop", "mobile") for theme in ("dark", "light"))
        cards.append(f'<section><h2>{title}</h2><div class="images">{images}</div></section>')
    (output / "index.html").write_text('''<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>LANBridge — новые экраны</title><style>body{margin:0;padding:32px;background:#0B0D11;color:#EFF3FA;font:14px system-ui}main{max-width:1500px;margin:auto}h1{font-size:30px}p{color:#95A2B4;line-height:1.7}section{margin:40px 0}.images{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px}a{display:grid;gap:12px;color:#95A2B4;text-decoration:none;border:1px solid #283240;border-radius:8px;padding:16px;background:#12161D}img{width:100%;height:360px;object-fit:contain;object-position:top}h2{font-size:21px}@media(max-width:650px){body{padding:20px}.images{grid-template-columns:1fr}}</style><main><h1>LANBridge · новые экраны</h1><p>Снимки запущенного Go-приложения на временных данных. Нажмите изображение для полного размера. На экране устройств — результат проверки адреса самого тестового сервера. Вход, первый запуск и настройки работают.</p>''' + "".join(cards) + "</main></html>", encoding="utf-8")
    sheet = Image.new("RGB", (1500, 1150), "#0B0D11")
    draw = ImageDraw.Draw(sheet)
    font = ImageFont.load_default(size=20)
    for index, (key, _) in enumerate(names):
        col, row = index % 3, index // 3
        x, y = 20 + col * 495, 20 + row * 560
        draw.text((x, y), key.upper(), font=font, fill="#95A2B4")
        with Image.open(output / f"{key}-desktop-dark.png") as original:
            original.thumbnail((470, 330))
            sheet.paste(original, (x, y + 35))
        for offset, theme in enumerate(("dark", "light")):
            with Image.open(output / f"{key}-mobile-{theme}.png") as original:
                original.thumbnail((190, 165))
                sheet.paste(original, (x + offset * 210, y + 370))
    sheet.save(output / "contact-sheet.png")
    print(json.dumps({"first_setup_ready_ms_local_chromium": ready_ms,
                      "initial_frontend_gzip_bytes_excluding_fonts": sum(len(gzip.compress(p.read_bytes(), mtime=0)) for p in assets if p.relative_to(ROOT/"web/dist").as_posix() in initial_paths),
                      "all_frontend_gzip_bytes_excluding_fonts": sum(len(gzip.compress(p.read_bytes(), mtime=0)) for p in assets),
                      "screenshots": 20, "external_requests": 0}, indent=2))
    print("PASS: setup/login/logout, SSE, persisted settings, mobile navigation, both themes, no page errors")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Exercise the real DEV fixture UI; never enables a real NAS or sender.

Requires Playwright Python and Chromium. Start Vite with the documented fixture
flag. Warm-up dependency reloads are recorded separately from measured runs.
"""
import argparse
import asyncio
import json
import re
from pathlib import Path
from urllib.parse import urlparse

from playwright.async_api import async_playwright

COPY = {
    'en-US': dict(operator='Operator', negative='Does not equal', match='Match value', rules='Rules', quiet='Defer during quiet hours', start='Start', end='End', zone='IANA time zone', save='Save configuration', preview='Preview without sending', and_op='AND', add='Add', airsim='Allow AirSIM iPhones'),
    'zh-CN': dict(operator='运算符', negative='不等于', match='匹配值', rules='规则', quiet='免打扰期间延后投递', start='开始', end='结束', zone='IANA 时区', save='保存配置', preview='预览（不发送）', and_op='AND', add='添加', airsim='允许 AirSIM iPhone 接入'),
}

async def select_option(page, scope, label, option):
    await scope.get_by_role('button', name=re.compile('^' + re.escape(label) + ':')).first.click()
    await page.get_by_role('option', name=option, exact=True).click()

async def measure(browser, base, output, width, locale):
    context = await browser.new_context(viewport={'width': width, 'height': 900}, locale=locale)
    page = await context.new_page()
    errors, writes = [], []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.on('request', lambda request: writes.append(request.url) if request.method not in ('GET', 'HEAD', 'OPTIONS') and urlparse(request.url).path.startswith('/api/') else None)
    text = COPY[locale]
    checks = []
    await page.goto(base + '/settings/bark')
    await page.wait_for_url('**/settings/forwarding')
    pane = page.locator('.forwarding-panel')
    await pane.locator('.forward-tabs').wait_for()
    await page.wait_for_load_state('networkidle')
    save = pane.get_by_role('button', name=text['save'], exact=True)
    assert await save.is_disabled(), 'Rendering a missing optional secret changed the saved configuration'
    assert not await pane.locator('input[type=checkbox]').first.is_checked()
    checks.extend(['legacy-bark-link', 'default-off', 'render-does-not-dirty-configuration'])

    await pane.locator('.forward-tabs').get_by_role('button', name=text['rules'], exact=True).click()
    condition = pane.locator('.forward-condition').first
    await select_option(page, condition, text['operator'], text['negative'])
    match = condition.get_by_label(text['match'], exact=True)
    await match.fill('10086||10000')
    assert await match.input_value() == '10086||10000'
    assert await match.evaluate('(element) => Boolean(element.labels && element.labels.length)')
    checks.extend(['negative-keyword-editor', 'native-match-label'])

    await pane.get_by_label(text['quiet'], exact=True).check()
    await pane.get_by_label(text['start'], exact=True).fill('22:00')
    await pane.get_by_label(text['end'], exact=True).fill('08:00')
    await pane.get_by_label(text['zone'], exact=True).fill('Asia/Shanghai')
    assert await pane.get_by_label(text['end'], exact=True).input_value() == '08:00'
    checks.append('overnight-quiet-hours-and-zone')
    await select_option(page, condition, text['operator'], text['and_op'])
    await condition.get_by_role('button', name=text['add'], exact=True).click()
    assert await pane.locator('.forward-condition').count() == 3
    checks.append('nested-condition-editor')

    await save.click()
    await pane.get_by_role('alert').wait_for()
    assert 'Demo mode' in await pane.get_by_role('alert').inner_text()
    checks.append('fixture-save-refused')
    await pane.get_by_role('button', name=text['preview'], exact=True).click()
    await pane.get_by_role('alert').wait_for()
    assert 'not evaluated' in await pane.get_by_role('alert').inner_text()
    checks.append('fixture-preview-does-not-claim-rule-execution')
    assert not await page.evaluate('document.documentElement.scrollWidth > innerWidth + 1'), 'horizontal overflow'
    await page.screenshot(path=str(output / f'forwarding-{locale}-{width}.png'), full_page=True)
    checks.append('narrow-or-desktop-layout')

    await page.goto(base + '/settings/sms-forward')
    await page.wait_for_url('**/settings/forwarding')
    await page.locator('.forwarding-panel .forward-tabs').wait_for()
    checks.append('legacy-sms-link')

    await page.goto(base + '/settings/airsim')
    await page.locator('.airsim-panel').wait_for()
    await page.get_by_role('switch', name=re.compile('^' + re.escape(text['airsim']))).check()
    choices = page.locator('.airsim-panel__lines input[type=checkbox]')
    assert await choices.count() >= 2, 'fixture did not expose multiple lines'
    await choices.nth(0).check()
    await choices.nth(1).check()
    assert await choices.nth(0).is_checked() and await choices.nth(1).is_checked()
    checks.append('airsim-multiline-selection')
    assert not writes, f'fixture caused network mutations: {writes}'
    assert not errors, f'browser runtime errors: {errors}'
    checks.extend(['no-real-api-writes', 'no-measured-runtime-errors'])
    await context.close()
    return dict(width=width, locale=locale, passed=True, checks=checks, physical_device=False)

async def main(args):
    args.output.mkdir(parents=True, exist_ok=False)
    async with async_playwright() as runtime:
        browser = await runtime.chromium.launch(executable_path=args.chromium, headless=True, args=['--no-sandbox'])
        warm = await browser.new_page(locale='en-US')
        warm_errors = []
        warm.on('pageerror', lambda error: warm_errors.append(str(error)))
        for route, selector in [('/settings/forwarding', '.forwarding-panel .forward-tabs'), ('/settings/airsim', '.airsim-panel')]:
            await warm.goto(args.base_url.rstrip('/') + route)
            await warm.wait_for_selector(selector)
            await warm.wait_for_load_state('networkidle')
        await warm.close()
        rows = []
        for locale in COPY:
            for width in (320, 390, 1280):
                rows.append(await measure(browser, args.base_url.rstrip('/'), args.output, width, locale))
        report = dict(kind='actual-fixture-browser-interactions', browser=browser.version, warmup_page_errors=warm_errors, cold_start_acceptance=False, results=rows, assertions=sum(len(row['checks']) for row in rows), real_nas=False, real_notification_service=False, production_build_browser_tested=False)
        (args.output / 'result.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        print(json.dumps(report, ensure_ascii=False))
        await browser.close()

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://127.0.0.1:5174')
    parser.add_argument('--chromium', default='/usr/bin/chromium')
    parser.add_argument('--output', type=Path, required=True)
    asyncio.run(main(parser.parse_args()))

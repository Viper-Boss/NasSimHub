# -*- coding: utf-8 -*-
"""Insert metricsChannel* keys after metricsUnavailable in every locale file."""
import io
import os

LOCALES = os.path.join(os.path.dirname(__file__), "..", "web", "src", "i18n", "locales")

TRANSLATIONS = {
    "zh-CN": ("模组通道异常，显示最近一次数据", "模组通道不可达"),
    "zh-TW": ("模組通道異常，顯示最近一次資料", "模組通道不可達"),
    "en-US": ("Module channel degraded, showing last known values", "Module channel unreachable"),
    "ja-JP": ("モジュールチャネル異常、最後の値を表示中", "モジュールチャネルに接続できません"),
    "de-DE": ("Modulkanal gestört, letzte Werte werden angezeigt", "Modulkanal nicht erreichbar"),
    "es-ES": ("Canal del módulo degradado, se muestran los últimos valores", "Canal del módulo inaccesible"),
    "fr-FR": ("Canal module dégradé, dernières valeurs affichées", "Canal module injoignable"),
    "pt-BR": ("Canal do módulo degradado, exibindo últimos valores", "Canal do módulo inacessível"),
    "vi-VN": ("Kênh module lỗi, hiển thị giá trị gần nhất", "Không thể kết nối kênh module"),
}

def main():
    for locale, (stale, down) in TRANSLATIONS.items():
        path = os.path.join(LOCALES, locale + ".ts")
        with io.open(path, "r", encoding="utf-8") as handle:
            text = handle.read()
        if "metricsChannelStale" in text:
            print(locale, "already patched")
            continue
        anchor = "metricsUnavailable:"
        index = text.find(anchor)
        if index < 0:
            raise SystemExit("anchor not found in " + locale)
        # Find the end of the metricsUnavailable value (the closing quote).
        quote_start = text.find("'", index)
        quote_end = text.find("'", quote_start + 1)
        if quote_start < 0 or quote_end < 0:
            raise SystemExit("value end not found in " + locale)
        insert_at = quote_end + 1
        addition = ", metricsChannelStale: '%s', metricsChannelDown: '%s'" % (stale, down)
        text = text[:insert_at] + addition + text[insert_at:]
        with io.open(path, "w", encoding="utf-8", newline="") as handle:
            handle.write(text)
        print(locale, "patched")

if __name__ == "__main__":
    main()

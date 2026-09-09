"""Add dashboard metrics + serving-cell i18n keys to all 9 locale files."""
import io
import re

BASE = r"C:/Users/wzs11/.zcode/workspace/default/nas-comm-gateway-r6/web/src/i18n/locales"

DASH = {
    "zh-CN": "metricsThermal: '温度', metricsCpuTemp: 'CPU 温度', metricsModel: '型号', metricsCores: '核心数', metricsFreq: '频率', metricsCache: '缓存', metricsProcesses: '进程', metricsThreads: '线程', metricsCapacity: '容量', metricsUsed: '已用', metricsAvailable: '可用', metricsCached: '缓存占用', metricsBuffers: '缓冲', metricsMemSpec: '类型/频率', metricsDevice: '设备', metricsFs: '文件系统', metricsFree: '剩余', metricsMount: '挂载点',",
    "zh-TW": "metricsThermal: '溫度', metricsCpuTemp: 'CPU 溫度', metricsModel: '型號', metricsCores: '核心數', metricsFreq: '頻率', metricsCache: '快取', metricsProcesses: '行程', metricsThreads: '執行緒', metricsCapacity: '容量', metricsUsed: '已用', metricsAvailable: '可用', metricsCached: '快取占用', metricsBuffers: '緩衝', metricsMemSpec: '類型/頻率', metricsDevice: '裝置', metricsFs: '檔案系統', metricsFree: '剩餘', metricsMount: '掛載點',",
    "en-US": "metricsThermal: 'Temperature', metricsCpuTemp: 'CPU temp', metricsModel: 'Model', metricsCores: 'Cores', metricsFreq: 'Frequency', metricsCache: 'Cache', metricsProcesses: 'Processes', metricsThreads: 'Threads', metricsCapacity: 'Capacity', metricsUsed: 'Used', metricsAvailable: 'Available', metricsCached: 'Cached', metricsBuffers: 'Buffers', metricsMemSpec: 'Type/Freq', metricsDevice: 'Device', metricsFs: 'Filesystem', metricsFree: 'Free', metricsMount: 'Mount point',",
    "de-DE": "metricsThermal: 'Temperatur', metricsCpuTemp: 'CPU-Temp', metricsModel: 'Modell', metricsCores: 'Kerne', metricsFreq: 'Frequenz', metricsCache: 'Cache', metricsProcesses: 'Prozesse', metricsThreads: 'Threads', metricsCapacity: 'Kapazität', metricsUsed: 'Belegt', metricsAvailable: 'Verfügbar', metricsCached: 'Cache', metricsBuffers: 'Puffer', metricsMemSpec: 'Typ/Freq.', metricsDevice: 'Gerät', metricsFs: 'Dateisystem', metricsFree: 'Frei', metricsMount: 'Einhängepunkt',",
    "es-ES": "metricsThermal: 'Temperatura', metricsCpuTemp: 'Temp. CPU', metricsModel: 'Modelo', metricsCores: 'Núcleos', metricsFreq: 'Frecuencia', metricsCache: 'Caché', metricsProcesses: 'Procesos', metricsThreads: 'Hilos', metricsCapacity: 'Capacidad', metricsUsed: 'En uso', metricsAvailable: 'Disponible', metricsCached: 'En caché', metricsBuffers: 'Búfer', metricsMemSpec: 'Tipo/Frec.', metricsDevice: 'Dispositivo', metricsFs: 'Sist. archivos', metricsFree: 'Libre', metricsMount: 'Punto montaje',",
    "fr-FR": "metricsThermal: 'Température', metricsCpuTemp: 'Temp. CPU', metricsModel: 'Modèle', metricsCores: 'Cœurs', metricsFreq: 'Fréquence', metricsCache: 'Cache', metricsProcesses: 'Processus', metricsThreads: 'Threads', metricsCapacity: 'Capacité', metricsUsed: 'Utilisé', metricsAvailable: 'Disponible', metricsCached: 'En cache', metricsBuffers: 'Tampons', metricsMemSpec: 'Type/Freq.', metricsDevice: 'Périphérique', metricsFs: 'Syst. fichiers', metricsFree: 'Libre', metricsMount: 'Point montage',",
    "ja-JP": "metricsThermal: '温度', metricsCpuTemp: 'CPU温度', metricsModel: 'モデル', metricsCores: 'コア数', metricsFreq: '周波数', metricsCache: 'キャッシュ', metricsProcesses: 'プロセス', metricsThreads: 'スレッド', metricsCapacity: '容量', metricsUsed: '使用中', metricsAvailable: '利用可能', metricsCached: 'キャッシュ済み', metricsBuffers: 'バッファ', metricsMemSpec: '種類/周波数', metricsDevice: 'デバイス', metricsFs: 'ファイルシステム', metricsFree: '空き', metricsMount: 'マウント',",
    "pt-BR": "metricsThermal: 'Temperatura', metricsCpuTemp: 'Temp. CPU', metricsModel: 'Modelo', metricsCores: 'Núcleos', metricsFreq: 'Frequência', metricsCache: 'Cache', metricsProcesses: 'Processos', metricsThreads: 'Threads', metricsCapacity: 'Capacidade', metricsUsed: 'Em uso', metricsAvailable: 'Disponível', metricsCached: 'Em cache', metricsBuffers: 'Buffers', metricsMemSpec: 'Tipo/Freq.', metricsDevice: 'Dispositivo', metricsFs: 'Sist. arquivos', metricsFree: 'Livre', metricsMount: 'Ponto montagem',",
    "vi-VN": "metricsThermal: 'Nhiệt độ', metricsCpuTemp: 'Nhiệt độ CPU', metricsModel: 'Model', metricsCores: 'Số lõi', metricsFreq: 'Tần số', metricsCache: 'Bộ nhớ đệm', metricsProcesses: 'Tiến trình', metricsThreads: 'Luồng', metricsCapacity: 'Dung lượng', metricsUsed: 'Đã dùng', metricsAvailable: 'Khả dụng', metricsCached: 'Trong bộ đệm', metricsBuffers: 'Bộ đệm', metricsMemSpec: 'Loại/Tần số', metricsDevice: 'Thiết bị', metricsFs: 'Hệ tệp', metricsFree: 'Còn trống', metricsMount: 'Điểm gắn',",
}

DEVICE = {
    "zh-CN": "servingFreq: '当前频率', servingBand: '频段', servingBandwidth: '带宽', servingCellId: '小区 ID', servingTac: 'TAC',",
    "zh-TW": "servingFreq: '當前頻率', servingBand: '頻段', servingBandwidth: '頻寬', servingCellId: '小區 ID', servingTac: 'TAC',",
    "en-US": "servingFreq: 'Current frequency', servingBand: 'Band', servingBandwidth: 'Bandwidth', servingCellId: 'Cell ID', servingTac: 'TAC',",
    "de-DE": "servingFreq: 'Aktuelle Frequenz', servingBand: 'Band', servingBandwidth: 'Bandbreite', servingCellId: 'Zell-ID', servingTac: 'TAC',",
    "es-ES": "servingFreq: 'Frecuencia actual', servingBand: 'Banda', servingBandwidth: 'Ancho de banda', servingCellId: 'ID de celda', servingTac: 'TAC',",
    "fr-FR": "servingFreq: 'Fréquence actuelle', servingBand: 'Bande', servingBandwidth: 'Bande passante', servingCellId: 'ID cellule', servingTac: 'TAC',",
    "ja-JP": "servingFreq: '現在の周波数', servingBand: '帯域', servingBandwidth: '帯域幅', servingCellId: 'セルID', servingTac: 'TAC',",
    "pt-BR": "servingFreq: 'Frequência atual', servingBand: 'Banda', servingBandwidth: 'Largura de banda', servingCellId: 'ID da célula', servingTac: 'TAC',",
    "vi-VN": "servingFreq: 'Tần số hiện tại', servingBand: 'Băng tần', servingBandwidth: 'Băng thông', servingCellId: 'ID ô', servingTac: 'TAC',",
}

for locale in DASH:
    path = f"{BASE}/{locale}.ts"
    with io.open(path, encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    out = []
    dash_done = device_done = False
    for line in lines:
        out.append(line)
        if not dash_done and re.search(r"metricsUnavailable", line):
            indent = re.match(r"^(\s*)", line).group(1)
            out.append(indent + DASH[locale])
            dash_done = True
        if not device_done and re.match(r"^\s*(\"?flightMode\"?)", line):
            indent = re.match(r"^(\s*)", line).group(1)
            out.append(indent + DEVICE[locale])
            device_done = True
    if not dash_done or not device_done:
        print(f"MISS in {locale}: dash={dash_done} device={device_done}")
        continue
    with io.open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(out) + "\n")
    print(f"OK {locale}")

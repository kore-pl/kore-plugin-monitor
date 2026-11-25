# kore-plugin-monitor (Plugin für Systemüberwachung)

## Was es tut

Liest echte Linux-Daten aus /proc und /sys und liefert bei Bedarf einen Systemüberblick, die Top-Prozesse, Datenträgerauslastung und Temperaturen. Produktionsreif (depth_level=production), abhängigkeitsfreie Basiskomponente.

Nur-Lese-Methoden deklarieren system:read.

## Wichtige Punkte

- Kein Polling (G-19): kein Ticker, kein AfterFunc und keine Hintergrund-Goroutine. CPU und Prozess-CPU% werden innerhalb einer Anfrage zweimal mit time.Sleep(200ms) dazwischen gemessen; danach kehrt der Aufruf zurück. Verlaufspunkte werden nur angehängt, wenn monitor.system aufgerufen wird und das Intervall abgelaufen ist — im Leerlauf kostet das null CPU.
- Schwellenwert-Warnungen werden nur zurückgegeben: alerts[] enthält Metrik, aktuellen Wert, Schwelle, Stufe und einen umsetzbaren Hinweis; das Plugin sendet keine Benachrichtigung, E-Mail oder Webhook und tätigt keine Netzwerkaufrufe (G-10). Benachrichtigungen gehören zum Alarmzentrum des Kerns.
- Keine Fremdabhängigkeiten: nur github.com/pengtang1024/kore/pkg/pluginapi und die Standardbibliothek.
- Methoden: monitor.system, monitor.processes, monitor.disks, monitor.temperature (vollständige Tabellen in DETAIL.md).

## Installation und Prüfung

Installieren Sie `monitor` über den Plugin-Markt oder importieren Sie das mit make dist erzeugte dist/monitor-0.5.0.tar.gz. Prüfen Sie mit make check (gofmt, go vet, go test -timeout 200s), einschließlich boundary- und fault-Tests.

---

简体中文原文见 [DETAIL.md](DETAIL.md)。

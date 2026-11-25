# kore-plugin-monitor (plugin de monitorización del sistema)

## Qué hace

Lee datos reales de /proc y /sys en Linux y devuelve, cuando se solicita, un resumen del sistema, los procesos con mayor consumo, uso de disco y temperaturas. Grado de producción (depth_level=production), componente básico sin dependencias.

Los métodos de solo lectura declaran system:read.

## Puntos clave

- Sin sondeo (G-19): ni ticker, ni AfterFunc, ni gorutinas en segundo plano. CPU y CPU% por proceso toman dos muestras dentro de una misma petición con time.Sleep(200ms) entre ellas; luego la llamada devuelve. Los puntos históricos se añaden solo cuando se llama a monitor.system y ha transcurrido el intervalo, así que en reposo el coste de CPU es cero.
- Las alertas de umbral solo se devuelven: alerts[] incluye métrica, valor actual, umbral, nivel y una sugerencia accionable; el plugin no envía notificaciones, correos ni webhooks y no sale a la red (G-10). Las notificaciones corresponden al centro de alertas del núcleo.
- Sin dependencias externas: solo github.com/pengtang1024/kore/pkg/pluginapi y la biblioteca estándar.
- Métodos: monitor.system, monitor.processes, monitor.disks, monitor.temperature (tablas completas en DETAIL.md).

## Instalación y verificación

Instale `monitor` desde el mercado de plugins o importe dist/monitor-0.5.0.tar.gz generado con make dist. Verifique con make check (gofmt, go vet, go test -timeout 200s), incluidas las suites boundary y fault.

---

简体中文原文见 [DETAIL.md](DETAIL.md)。

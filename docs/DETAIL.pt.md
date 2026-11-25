# kore-plugin-monitor (plugin de monitoramento do sistema)

## O que faz

Lê dados reais de /proc e /sys do Linux e retorna sob demanda uma visão geral do sistema, os processos com maior consumo, uso de disco e temperaturas. Grau de produção (depth_level=production), componente básico sem dependências.

Métodos somente leitura declaram system:read.

## Pontos-chave

- Sem polling (G-19): sem ticker, AfterFunc ou goroutines em segundo plano. CPU e CPU% por processo coletam duas amostras dentro de uma mesma requisição com time.Sleep(200ms) entre elas; a chamada então retorna. Pontos históricos só são acrescentados quando monitor.system é chamado e o intervalo decorreu, portanto em repouso o custo de CPU é zero.
- Alertas de limite apenas são retornados: alerts[] traz métrica, valor atual, limite, nível e uma dica acionável; o plugin não envia notificação, e-mail ou webhook e não faz chamadas de rede (G-10). Notificações são do centro de alertas do núcleo.
- Sem dependências externas: apenas github.com/pengtang1024/kore/pkg/pluginapi e a biblioteca padrão.
- Métodos: monitor.system, monitor.processes, monitor.disks, monitor.temperature (tabelas completas em DETAIL.md).

## Instalação e verificação

Instale `monitor` no mercado de plugins ou importe dist/monitor-0.5.0.tar.gz gerado por make dist. Verifique com make check (gofmt, go vet, go test -timeout 200s), incluindo as suítes boundary e fault.

---

简体中文原文见 [DETAIL.md](DETAIL.md)。

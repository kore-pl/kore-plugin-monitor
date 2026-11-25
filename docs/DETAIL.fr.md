# kore-plugin-monitor (plugin de surveillance système)

## Ce que fait le plugin

Lit les vraies données Linux /proc et /sys et renvoie à la demande un aperçu du système, les principaux processus, l'utilisation des disques et les températures. Qualité production (depth_level=production), composant de base sans dépendances.

Les méthodes en lecture seule déclarent system:read.

## Points clés

- Aucun polling (G-19) : ni ticker, ni AfterFunc, ni goroutine d'arrière-plan. Le CPU et le CPU% par processus prennent deux échantillons dans une même requête avec time.Sleep(200ms) entre eux ; l'appel revient ensuite. Les points d'historique ne sont ajoutés que lorsque monitor.system est appelé et que l'intervalle s'est écoulé : au repos, le coût CPU est nul.
- Les alertes de seuil sont uniquement renvoyées : alerts[] contient la métrique, la valeur actuelle, le seuil, le niveau et une piste actionnable ; le plugin n'envoie aucune notification, aucun e-mail ni webhook et ne fait aucun appel réseau (G-10). Les notifications relèvent du centre d'alertes du noyau.
- Aucune dépendance externe : seulement github.com/pengtang1024/kore/pkg/pluginapi et la bibliothèque standard.
- Méthodes : monitor.system, monitor.processes, monitor.disks, monitor.temperature (tableaux complets dans DETAIL.md).

## Installation et vérification

Installez `monitor` depuis le marché de plugins ou importez dist/monitor-0.5.0.tar.gz produit par make dist. Vérifiez avec make check (gofmt, go vet, go test -timeout 200s), y compris les suites boundary et fault.

---

简体中文原文见 [DETAIL.md](DETAIL.md)。

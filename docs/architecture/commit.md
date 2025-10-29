# Commit-Orchestrierung

Dieses Dokument beschreibt das Commit-Protokoll der Queue-Banken und den orchestrierten Ablauf im Commit-Orchestrator.

## Stop-the-world-Phase

* **Globale Sperre:** `CommitAll` erwirbt eine Prozess-weite Mutex. Solange die Sperre gehalten wird, können keine weiteren Writer (`CommitAll`-Aufrufe) starten und Reader sehen weiterhin den letzten vollständig committed Zustand.
* **Frozen Reader:** Während die Sperre gehalten wird, wird kein neuer Versionsstand veröffentlicht. Reader greifen somit weiter auf den bisherigen Snapshot zu.
* **Kontextunterstützung:** Der Aufruf akzeptiert einen `context.Context`. Kontext-Abbrüche werden während der Stop-the-world-Phase geprüft, um blockierende Commits rechtzeitig abzubrechen.

## Reihenfolge der Bank-Commits

1. Die Banks werden in der Reihenfolge verarbeitet, in der sie beim Orchestrator registriert wurden.
2. Jeder Bank-Commit wird synchron ausgeführt, solange die globale Sperre gehalten wird.
3. Erst nach erfolgreichem Abschluss aller Banken wird die neue Version veröffentlicht und Reader wechseln auf den neuen Snapshot.

## Fehlerbehandlung

* **Kurzschluss:** Schlägt ein Bank-Commit fehl, wird die Verarbeitung sofort abgebrochen. Weitere Banken werden nicht mehr aufgerufen.
* **Rollback durch Auslassung:** Da Leser erst nach erfolgreichem Abschluss aller Banken umgeschaltet werden, verbleiben sie beim vorherigen konsistenten Zustand.
* **Metriken & Tracing:** Jeder Commit-Versuch meldet Dauer und Fehlversuche an `internal/telemetry/commit_metrics.go`. Fehlversuche werden gezählt und können von außen beobachtet werden.
* **Fehlerpropagierung:** Der Fehler der Bank wird unverändert an den Aufrufer von `CommitAll` zurückgegeben. Zusätzlich bricht ein abgebrochener Kontext den Commit mit dem jeweiligen Kontextfehler ab.

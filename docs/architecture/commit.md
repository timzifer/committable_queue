# Commit-Orchestrierung

Dieses Dokument beschreibt das Commit-Protokoll der Queue-Banken und den orchestrierten Ablauf im Commit-Orchestrator.

## Stop-the-world-Phase

* **Globale Sperre:** `CommitAll` erwirbt eine Prozess-weite Mutex. Solange die Sperre gehalten wird, können keine weiteren Writer (`CommitAll`-Aufrufe) starten und Reader sehen weiterhin den letzten vollständig committed Zustand.
* **Frozen Reader:** Während die Sperre gehalten wird, wird kein neuer Versionsstand veröffentlicht. Reader greifen somit weiter auf den bisherigen Snapshot zu.
* **Kontextunterstützung:** Der Aufruf akzeptiert einen `context.Context`. Kontext-Abbrüche werden während der Stop-the-world-Phase geprüft, um blockierende Commits rechtzeitig abzubrechen.

## Zwei-Phasen-Protokoll

1. **Prepare:** Jede Bank implementiert `PrepareCommit`. Der Orchestrator ruft diese Funktion seriell auf und sammelt Publish-/Abort-Callbacks. In dieser Phase werden Pending-Segmente lediglich ausgestaged – sichtbare Daten bleiben unverändert.
2. **Abort bei Fehlern:** Tritt während `PrepareCommit` ein Fehler auf oder wird der Kontext abgebrochen, führt der Orchestrator alle bis dahin gesammelten Abort-Callbacks in umgekehrter Reihenfolge aus. Dadurch werden ausgestagte Daten wieder in den Pending-Bereich zurückgeführt.
3. **Publish:** Nur wenn alle Banken erfolgreich vorbereitet wurden und der Kontext weiterhin aktiv ist, ruft der Orchestrator die Publish-Callbacks in Registrierungsreihenfolge auf. Erst jetzt werden neue Daten sichtbar gemacht.
4. **Version erhöhen:** Nach erfolgreichem Publish aller Banken wird die globale Version atomar erhöht und Reader können auf den neuen Snapshot wechseln.

## Fehlerbehandlung

* **Kurzschluss:** Schlägt eine `PrepareCommit`-Phase fehl, werden verbleibende Banken nicht mehr aufgerufen und alle bereits vorbereiteten Banken rollen ihren Zustand via Abort zurück.
* **Garantiertes Rollback:** Da das Publish erst nach erfolgreichem Abschluss aller Prepare-Phasen erfolgt, können keine partiellen Zustände sichtbar werden. Aborts stellen sicher, dass alle Pending-Daten erhalten bleiben.
* **Metriken & Tracing:** Jeder Commit-Versuch meldet Dauer und Fehlversuche an `internal/telemetry/commit_metrics.go`. Fehlversuche werden gezählt und können von außen beobachtet werden.
* **Fehlerpropagierung:** Der Fehler der Bank wird unverändert an den Aufrufer von `CommitAll` zurückgegeben. Zusätzlich bricht ein abgebrochener Kontext den Commit mit dem jeweiligen Kontextfehler ab.

## Deterministische Mehrregister-Lesevorgänge

* **Versionskonsistenz:** Multi-Register-Reader (z. B. Modbus-Clients) dürfen nur Wertepaare verarbeiten, deren `Version` und `Timestamp` identisch sind. Sichtbare Register werden erst nach erfolgreichem `CommitAll` aktualisiert.
* **Staging der Writer:** Writer aktualisieren pro Bank zunächst Pending-Register. `PrepareCommit` übernimmt diese Werte nur temporär; bei einem Fehlschlag werden sie wieder in Pending überführt.
* **Sichtbarkeit:** Während `CommitAll` läuft, bleiben Reader auf dem zuletzt veröffentlichten Snapshot. Erst wenn alle Banken published und die globale Version erhöht wurde, erscheinen die neuen Registerwerte atomar für alle beteiligten Banken.

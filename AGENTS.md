# Istruzioni per la prossima sessione

## Stato e obiettivo

Agent implementato secondo `docs/CONTRACT.md`, con gateway HTTP S3-only autorizzato dopo l'indagine IAM. Leggere `README.md`, `docs/OPERATIONS.md` e `docs/VERIFICATION.md`. Il backend diretto conserva i limiti in `docs/COMPATIBILITY.md`: non rimuovere il confine di rete né indebolire i test. `docs/DESIGN-HISTORY.md` è solo lo storico.

Stessa architettura di `../agent-proxmox-fae`, con stdagent e client RustFS separato. Non modificare core, libreria comune o altri repository.

## Come riprendere

- Implementare il contratto approvato. Per nuove decisioni o incompatibilità fermarsi e fare una domanda alla volta, con raccomandazione e compromessi.
- Rispondere tramite ispezione del codice alle domande che il codice può risolvere; non chiederle all'utente.
- Concordare un perimetro e criteri verificabili prima di scrivere l'agent.
- Non ampliare automaticamente lo scope alla gestione di Docker, host, cluster o alta disponibilità del backend.
- Non modificare `../agent-mysql` o gli altri repository senza autorizzazione.

## Riferimenti locali

- `../agent-lib-go/pkg/agent/agent.go`: interfacce e modelli dell'agent.
- `../agent-lib-go/pkg/stdagent/`: polling dei job, health, metriche e risoluzione dei secret.
- `../agent-proxmox-fae/internal/vmagent/`: esempio di agent Go basato sulla libreria comune.
- `../agent-test/`: riferimento aggiuntivo, da confrontare con il protocollo attuale.
- `../core/pkg/domain/service_type.go` e `service_lifecycle.go`: schema delle proprietà e lifecycle configurabile.
- `../core/pkg/domain/service.go` e `job.go`: servizi, job e dati runtime dell'agent.
- `../core/docs/SERVICE_TYPE.md`: documentazione da verificare rispetto al codice.

Leggere eventuali `AGENTS.md` dei repository consultati. Il codice attuale prevale sugli esempi o README non aggiornati.

## Vincoli da preservare

- Un servizio S3 rappresenta un bucket su un backend condiviso, con credenziali ristrette a quel servizio; non un container per cliente.
- Il provider predispone il backend RustFS. L'agent gestisce bucket, credenziali e quote tramite API.
- RustFS è il candidato scelto per la valutazione, non un backend già validato per produzione.
- Verificare la release realmente scelta: la documentazione online può descrivere funzionalità non presenti in quella release.
- Nessun secret in Git, log o dati runtime non protetti; riusare i meccanismi del core dopo averne verificato le API effettive.
- Non assumere compatibilità S3 completa, quote invalicabili, durabilità o failover senza prove.
- Non costruire ora un framework generico per agent o runtime Docker. Estrarre codice comune solo quando un secondo uso concreto lo giustifica.

Approvati: Apache-2.0, agent-lib-go/utils, AWS SDK Go v2, client amministrativo interno, RustFS 1.0.4 fissato per digest e force delete. HTTP solo nel banco di prova isolato. Secret cliente ephemeral, mai nei log/runtime/disco dell'agent. Licenza di utils da chiarire prima della distribuzione.

Verifiche: `go test ./...`, `go test -race ./...`, `go vet ./...`, poi i comandi in `docs/VERIFICATION.md`. E2E usa un core pulito alla revisione in `tests/core-revision`, senza modificarlo. Non eseguire due suite E2E sullo stesso banco: il test ferma/riavvia il solo RustFS dedicato. Non stampare env, log grezzi o `docker compose config` senza `--quiet`. Niente secret o `.local`/`.pi` in Git; nessun push di una consegna completa senza evidenze.

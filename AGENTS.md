# Istruzioni per la prossima sessione

## Stato e obiettivo

Implementazione autorizzata del contratto in `docs/CONTRACT.md`, fermata al controllo preliminare IAM: leggere `docs/COMPATIBILITY.md` e ottenere la decisione aperta prima di scrivere l'agent. I test negativi sono attualmente fallenti e non vanno indeboliti. Leggere anche `README.md`; `docs/DESIGN-HISTORY.md` è solo lo storico.

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

Verifiche: `go test ./...`, `go test -race ./...`, `go vet ./...`, più test reali bloccanti indicati nel contratto. Nessun push di una consegna dichiarata completa senza evidenze; niente secret o dati locali in Git.

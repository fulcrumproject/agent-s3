# Verifiche della prima implementazione

Ambiente locale: Linux amd64, Go 1.27.0, Python 3.14.10, Docker 29.9.3,
Compose v5.7.0. Dati interamente sintetici. Nessuna modifica agli altri repository.

## Revisioni e dipendenze

- Core invariato: `3212fc7d41422be92b53997770d3311fcab8bf36` (checkout pulito).
- RustFS 1.0.4: indice OCI
  `sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2`.
- Gateway nginx 1.30.6-alpine:
  `sha256:583b7f2fce1cdb8da5ad5c39ebd3c33b98b87d493190e548bdee1fa8c619c311`.
- Builder Go 1.27.0-alpine:
  `sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc`.
- Dipendenze Go in go.mod/go.sum; boto3/AWS CLI e dipendenze in tests/requirements.txt.

Il commit esatto verificato va indicato nel record di consegna. I comandi sotto
sono riproducibili sul checkout di quel commit, non soltanto su uno screenshot di output.

## Controlli

| Controllo | Esito locale |
| --- | --- |
| go test ./... | Superato |
| go test -race ./... | Superato |
| go vet ./... | Superato |
| Compilazione sintattica degli script Python | Superata |
| pip check | Superato |
| Build immagine agent e avvio --help con filesystem read-only | Superati, utente 10001:10001 |
| tests/backend_contract.py attraverso gateway | Superato |
| tests/test_iam_boundaries.py attraverso gateway | 3 test superati |
| tests/network_boundary.sh | Rete cliente raggiunge gateway ma non backend diretto, neppure per IP |
| tests/e2e.py con core invariato e agent compilato con -race | Superato, nessuna segnalazione di race |

## Criteri e prove

1. **Creazione e ownership:** due servizi reali con nomi derivati, utenti e policy distinti;
   rinomina visualizzata senza cambio bucket, cambio endpoint rifiutato dallo schema.
   Le unit test provano retry senza duplicati, risorse estranee, policy drift, gruppi inattesi,
   UUID non validi e finestre di ownership incerta che devono fallire senza nuove modifiche.
2. **Secret:** configurazione amministrativa risolta dal vault; secret cliente eliminato dal
   vault dopo il normale completamento; nessun valore nei dati runtime o nei log catturati.
   Il logger è testato anche con byte JSON, gruppi, LogValuer e panic.
3. **Isolamento:** lettura/scrittura su altri bucket negate; accesso anonimo negato.
   API self-service/amministrative/STS bloccate dal gateway; alias e percorsi normalizzati
   sono testati anche con firma amministrativa valida. I limiti del backend diretto restano reali.
4. **Quota:** obbligatoria e riletta prima dell'abilitazione. PUT oltre limite, sovrascrittura
   rifiutata senza alterare l'oggetto esistente, copia oltre limite, otto scritture concorrenti
   da 256 KiB su quota 1 MiB, multipart oltre quota senza oggetto visibile. Aumento riuscito,
   riduzione fallita senza ridurre il limite backend. Non è una prova universale di quota invalicabile.
5. **Client e rotazione:** AWS CLI, boto3, chiavi URL-encoded, presigned PUT/GET e multipart a
   due parti da 5+1 MiB, con verifica dei contenuti e dell'ordine. Vecchio secret e vecchio URL rifiutati dopo la rotazione.
6. **Sospensione:** blocco delle richieste nuove, rotazione da sospeso senza riabilitazione,
   riavvio dell'agent senza secret cliente né perdita dello stato, resume riuscito.
7. **Force delete:** oltre 1000 oggetti, versioni, delete marker e multipart incompleto.
   Bucket, utente e policy assenti al termine. Legal hold reale: cancellazione parziale
   fallita, identità disabilitata, resume negato, hold non aggirato.
8. **Recupero osservato:** backend del banco fermato, agent in Error, nuovo job rimasto Pending;
   backend riavviato, agent Connected, hold e marcatore deleting persistenti. Il delete fallisce
   ancora finché l'operatore del test non rilascia il proprio hold; poi il retry esplicito completa.
   Le unit test coprono anche perdita dell'ack, errori di quota/IAM e risposte di delete senza effetti.
9. **Metriche:** i tre tipi vengono ricevuti dal core tramite stdagent. Valori mancanti o quota
   assente producono errore, non un campione zero; il rapporto non viene nascosto se supera 1.
10. **Consegna e confini:** esempi di schema validati dal core reale, binario compilato, immagine
    non-root costruita, gateway separato, nessuna gestione container nel runtime dell'agent.

## Comandi di accettazione

```sh
go test ./...
go test -race ./...
go vet ./...
.local/venv/bin/python tests/backend_contract.py
.local/venv/bin/python tests/test_iam_boundaries.py
bash tests/network_boundary.sh
go build -race -o .local/agent ./cmd/agent
.local/venv/bin/python -u tests/e2e.py
docker build -t fulcrum-agent-s3:dev .
docker run --rm --read-only --cap-drop ALL fulcrum-agent-s3:dev --help
```

Il test di paginazione non è un benchmark: usa concorrenza limitata e retry standard
limitati del client per i 503 transitori dell'accounting, senza rimuovere quote né
accettare upload mancanti. La prova può richiedere alcuni minuti.

## Pulizia ed esclusioni

I dati S3 creati dalle esecuzioni riuscite sono stati cancellati attraverso l'agent.
Il test rilascia soltanto il legal hold che ha creato; l'agent non invia flag di bypass.
I processi agent avviati dalla suite vengono terminati e i loro token revocati.
Metadati del core e volumi del banco rimangono disponibili localmente per ispezione;
nessun dato reale viene usato. Le credenziali del banco restano nei soli file ignorati 0600.

Non verificati né dichiarati: TLS, browser/CORS, altri client o release, cluster/HA,
crash consistency del filesystem, backup/restore, produzione, fatturazione e audit
completo del core. Il recupero di job Processing dopo crash resta quello preesistente
del core; non viene sostituito da un motore nuovo. La licenza di utils resta aperta
prima della distribuzione di binari/immagini.

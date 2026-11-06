# Operazioni e sicurezza

## Prerequisiti del provider

- RustFS 1.0.4 predisposto e raggiungibile dall'agent via API.
- Endpoint cliente separato, con il confine equivalente a `deploy/gateway.conf`.
  I clienti non devono poter raggiungere le porte native, la console o API compatibili MinIO.
- Una replica attiva per agentId. Riservare i nomi `fulcrum-<serviceId>`, `fc<serviceId senza trattini>`
  e `fc-<serviceId senza trattini>` alle risorse gestite; non modificarle contemporaneamente a mano.
- Mantenere lo stesso backend per l'intera vita dell'agent. Endpoint privato, endpoint cliente e region
  sono immutabili nello schema fornito. Per un backend diverso registrare un altro agent.
  Non sostituire il backend dietro lo stesso URL/DNS senza riconciliare i servizi: non è una migrazione
  gestita dall'agent, e non viene verificata un'identità fisica del cluster.
- Credenziali amministrative adatte alle operazioni richieste. Il banco usa credenziali amministrative
  casuali; non certifica una policy amministrativa di produzione a privilegi minimi.
- Vault del core configurato. L'agent non implementa un archivio di secret alternativo.
- Timeout del core coerenti con attesa in coda e durata delle operazioni.

HTTP è ammesso solo in questa fase isolata. Il gateway non cifra traffico e non rende
sicura un'esposizione Internet. Non è un deployment di produzione, un backup o un failover.

Il gateway preserva Host e URI per SigV4, inoltra le richieste S3 nel namespace
`fulcrum-*` e GET / per ListBuckets, bloccando le altre rotte e POST / (STS).
Non registra URL firmati. I controlli IAM restano necessari: il gateway non sostituisce
la policy di isolamento del bucket. Dopo la ricreazione del container backend con un
nuovo IP, riavviare/ricaricare il gateway; la fixture non implementa service discovery HA.

## Configurazione

Configurazione locale, prefisso `S3_AGENT_`:

| Variabile | Default |
| --- | --- |
| `FULCRUM_API_URL` | `http://127.0.0.1:8080` |
| `FULCRUM_API_TOKEN` | obbligatorio |
| `JOB_POLL_INTERVAL` | `5s` |
| `METRIC_REPORT_INTERVAL` | `30s` |
| `HEALTH_INTERVAL` | `15s` |

Le variabili effettive includono il prefisso, ad esempio `S3_AGENT_JOB_POLL_INTERVAL`.
Sono disponibili il file locale `-config`, il file ignorato `.env` e le opzioni di logging
delle utility Fulcrum. Nessun valore sensibile va inserito nel repository.

Configurazione remota `configuration.rustfs`:

- `endpoint`: origin HTTP(S) privata, senza userinfo, path, query o fragment;
- `clientEndpoint`: origin cliente distinta, protetta dal gateway/rete;
- `accessKey`, `secretKey`: secret persistenti amministrativi risolti dalla libreria;
- `region`: default `us-east-1`;
- `httpTimeoutSeconds`: default 15;
- `operationTimeoutSeconds`: default 180, almeno pari al timeout HTTP.

Credenziali e timeout si applicano alla connessione iniziale: riavviare dopo una modifica.
Endpoint e region sono immutabili nello schema della prima versione.
La normalizzazione degli URL non può dimostrare l'isolamento della rete: resta un requisito del provider.

## Provisioning e ownership

L'agent controlla prima l'assenza o l'appartenenza di bucket, policy e identità.
Il bucket porta tag con serviceId, agentId e fase; la policy contiene un marker di
ownership e deve coincidere con l'intero documento atteso. L'utente deve avere
esclusivamente quella policy e nessun gruppo. Il solo nome non prova l'appartenenza.

L'identità viene creata disabilitata, la policy viene associata esplicitamente,
la quota positiva viene applicata e riletta. Si attende la disponibilità delle statistiche
prima di abilitare l'accesso. Niente fallback illimitato.

Le API di creazione e tagging/binding non sono una transazione unica:
- crash dopo CreateBucket ma prima dei tag;
- crash dopo add-user ma prima del binding della policy.

In queste finestre un retry rifiuta l'adozione della risorsa incerta. Un operatore deve
verificare e riconciliare le sole risorse coinvolte prima di ritentare. Non aggiungere
fallback basati soltanto sul prefisso o cancellazioni ricorsive per risolvere il conflitto.

L'idempotenza riguarda gli handler sullo **stesso serviceId**. Due POST di creazione
del servizio nel core possono produrre due servizi distinti; non viene aggiunta
idempotenza alla API del core. Un core già in stato terminale può rifiutare ulteriori azioni.

## Secret e aggiornamenti

Usare un secret casuale forte generato dal cliente. Il minimo di compatibilità è 8 caratteri,
non una garanzia di entropia. L'agent riceve il valore dal vault solo per l'operazione;
non lo restituisce nei dati runtime, non lo salva localmente e non lo registra.

La rotazione sostituisce il secret del medesimo accessKeyId. Non c'è sovrapposizione.
Conserva lo stato disabilitato di un servizio sospeso. Non promette di annullare richieste
già in corso; gli URL prefirmati sono credenziali bearer da proteggere.

Quota e secret non formano una transazione distribuita. Un aggiornamento può avere effetti
parziali: il job deve fallire esplicitamente, senza rollback nascosto né riduzione della quota.
Il retry confronta il limite effettivo del backend; se serve il secret, il cliente reinvia
lo stesso valore desiderato. Non c'è fencing distribuito delle mutazioni RustFS: una richiesta
scaduta può avere esito incerto. Non inviare aggiornamenti confliggenti finché l'operatore
non ha riconciliato l'esito precedente.

Il core attuale applica le proprietà tentate anche sulle transizioni di errore dell'azione
`update`. Dopo un errore, la proprietà desiderata può differire dal backend: verificare
il job e le metriche/lo stato effettivo. Per esempio, dopo una riduzione rifiutata,
reinviare la quota effettiva prima di un altro aggiornamento.

## Cancellazione

`DELETE /api/v1/services/{id}` avvia **force delete**, senza conferme aggiuntive nell'agent.

1. Verifica ownership prima di qualsiasi modifica.
2. Scrive e rilegge il marcatore persistente `deleting`.
3. Disabilita l'identità e verifica lo stato prima di rimuovere dati.
4. Aborta multipart; elimina versioni e delete marker a pagine, controllando gli errori per oggetto.
5. Rimuove utente e policy, mantenendo il bucket come prova di ownership fino al termine della pulizia IAM.
6. Elimina il bucket e verifica l'assenza di tutte le risorse.

Non usa `BypassGovernanceRetention`. Con un hold/retention attivo può eliminare alcuni
oggetti e poi fallire: non ripristina i dati e non riattiva l'accesso. Il provider deve
risolvere il vincolo legittimamente; un nuovo delete riprende la pulizia.

Un errore prima della disabilitazione può lasciare l'accesso originale, ma non viene
iniziato lo svuotamento. Il marcatore impedisce a create/update/resume di riattivare un
bucket dopo l'avvio della cancellazione, anche se il core presenta uno stato vecchio.
Il marcatore non viene cancellato per aggirare un errore.

## Timeout e recupero ereditati dal core

`stdagent` non riprende automaticamente job Processing dopo un crash. Se abilitato,
il worker del core può marcarli Failed; poi occorre un nuovo tentativo esplicito.
Il timeout usa createdAt, non claimedAt. La procedura di timeout non applica transizioni
lifecycle né pulizia dei secret: `ephemeral` non è una garanzia di scadenza temporale.

Dopo CreateFailed, reinviare le proprietà necessarie con PATCH (nuova quota valida e secret,
quando richiesto). Le azioni generiche del core non trasportano nuovi parametri.
Un backend già interamente configurato può essere riconosciuto dopo una perdita dell'ack,
senza recuperare o memorizzare il secret.

Nessuna modifica a core o agent-lib-go viene effettuata per nascondere questi limiti.

## Salute e metriche

OnHealth controlla raggiungibilità/autenticazione del backend, non tutta la rete cliente
o lo stato di ogni nodo di un cluster. OnMetrics invia:
- `s3.storage.used_bytes`;
- `s3.storage.quota_bytes`;
- `s3.storage.usage_ratio` (non limitato artificialmente a 1).

Errori o campi mancanti non producono zeri inventati. I valori possono essere ritardati,
non misurano tutte le risorse fisiche e non sono certificati per fatturazione.
RustFS può rispondere 503 durante indisponibilità temporanee dell'accounting:
usare retry client limitati, senza disabilitare il controllo della quota.

## Fixture

Lo scanner del banco usa ciclo di 2 secondi e velocità fastest per ridurre le attese.
Questo non cambia la policy di quota e non è una raccomandazione di tuning produttivo.

Core, PostgreSQL e RustFS hanno volumi locali dedicati. Il bootstrap preesistente del core
dura 24 ore; un banco riutilizzato può richiedere nuove credenziali amministrative o una
ricreazione esplicita dei soli dati di prova. Nessuno script resetta un database condiviso.
La CI elimina soltanto i propri volumi al termine. Le prove non auditano tutta la sicurezza
del core né la licenza della dipendenza utils.
